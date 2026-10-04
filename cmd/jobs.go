package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/iamnikolie/gengoya/internal/config"
	"github.com/iamnikolie/gengoya/internal/jobs"
	"github.com/spf13/cobra"
)

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "Inspect and collect detached video jobs",
	Long: `Detached video jobs ('gengoya video --detach') are recorded under
~/.gengoya/<profile>/jobs/. Poll one with 'jobs status', download the finished
clip with 'jobs fetch'.`,
	Annotations: map[string]string{"kind": "video"},
}

var jobsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recorded video jobs, newest first",
	Args:  cobra.NoArgs,
	// Reads local job records only — no provider, no API key needed.
	Annotations: map[string]string{"kind": "video", "local": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		profileDir, err := config.ProfileDir(profile)
		if err != nil {
			return err
		}
		all, err := jobs.List(profileDir)
		if err != nil {
			return err
		}
		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			if all == nil {
				all = []jobs.Job{}
			}
			return enc.Encode(all)
		}
		if len(all) == 0 {
			fmt.Fprintln(stderr, "no jobs recorded")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATE\tMODEL\tRES\tDUR\tCOST\tCREATED\tPROMPT")
		for _, j := range all {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%ss\t%s\t%s\t%s\n",
				j.ID, j.State, j.Model, j.Resolution, j.Duration,
				costLabel(jobCost(j)),
				j.CreatedAt.Format(time.RFC3339), truncatePrompt(j.Prompt, 40))
		}
		return w.Flush()
	},
}

var jobsStatusCmd = &cobra.Command{
	Use:   "status <job-id|operation>",
	Short: "Poll a job once and print its state",
	Args:  cobra.ExactArgs(1),
	// The provider comes from the job record, not from the current flags, so
	// this command builds its own client instead of the pre-run one.
	Annotations: map[string]string{"kind": "video", "local": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		profileDir, err := config.ProfileDir(profile)
		if err != nil {
			return err
		}
		job, err := jobs.Resolve(profileDir, args[0])
		if err != nil {
			return err
		}
		vp, err := videoProviderFor(job.Provider)
		if err != nil {
			return err
		}
		op, err := vp.Poll(cmd.Context(), job.Operation)
		if err != nil {
			return err
		}

		switch {
		case op.Error != "":
			job.State = jobs.StateFailed
			job.Error = op.Error
		case op.Done && job.State != jobs.StateFetched:
			job.State = jobs.StateDone
		}
		if err := jobs.Save(profileDir, job); err != nil {
			return err
		}

		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"job_id":    job.ID,
				"operation": job.Operation,
				"state":     job.State,
				"done":      op.Done,
				"error":     job.Error,
				"paths":     job.Paths,
			})
		}
		fmt.Fprintf(stderr, "job=%s state=%s done=%t\n", job.ID, job.State, op.Done)
		if job.Error != "" {
			return fmt.Errorf("job %s failed: %s", job.ID, job.Error)
		}
		fmt.Fprintln(os.Stdout, job.State)
		return nil
	},
}

var jobsFetchCmd = &cobra.Command{
	Use:   "fetch <job-id|operation>",
	Short: "Wait for a job, download the clip, and print its path",
	Long: `Waits for the job to finish (respecting --timeout), downloads every clip
into the job's output directory, writes poster frames, and prints the paths.
Already-fetched jobs print their recorded paths without re-downloading.`,
	Args: cobra.ExactArgs(1),
	// The provider comes from the job record, not from the current flags, so
	// this command builds its own client instead of the pre-run one.
	Annotations: map[string]string{"kind": "video", "local": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		profileDir, err := config.ProfileDir(profile)
		if err != nil {
			return err
		}
		job, err := jobs.Resolve(profileDir, args[0])
		if err != nil {
			return err
		}
		if outDir != "." && outDir != "" {
			job.OutDir = outDir
		}
		if nameBase != "" {
			job.Name = nameBase
		}
		if cmd.Flags().Changed("poster-frames") {
			job.PosterFrames = posterFrames
		}

		if job.State == jobs.StateFetched && len(job.Paths) > 0 {
			fmt.Fprintf(stderr, "job=%s already fetched\n", job.ID)
			for _, p := range job.Paths {
				fmt.Fprintln(os.Stdout, p)
			}
			return nil
		}

		vp, err := videoProviderFor(job.Provider)
		if err != nil {
			return err
		}

		ctx, cancel := contextWithTimeout(cmd.Context(), videoTimeout)
		defer cancel()

		start := time.Now()
		arts, job, err := waitFetchWrite(ctx, vp, job, profileDir)
		if err != nil {
			return err
		}
		return emitVideoResult(arts, videoMetaFor(job, jobs.StateFetched, time.Since(start)), jsonOutput, os.Stdout, stderr)
	},
}

func truncatePrompt(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func init() {
	jobsFetchCmd.Flags().DurationVar(&videoPollInterval, "poll-interval", 10*time.Second, "operation poll interval")
	jobsFetchCmd.Flags().DurationVar(&videoTimeout, "timeout", 15*time.Minute, "give up waiting after this long (0 = no limit)")
	jobsFetchCmd.Flags().IntVar(&posterFrames, "poster-frames", 4, "PNG contact-sheet frames written next to the clip (0 = off)")
	jobsCmd.AddCommand(jobsListCmd, jobsStatusCmd, jobsFetchCmd)
	rootCmd.AddCommand(jobsCmd)
}

// jobCost is the job's actual cost when the provider reported usage, else the
// pre-call estimate.
func jobCost(j jobs.Job) float64 {
	if j.CostUSD > 0 {
		return j.CostUSD
	}
	return j.CostEstimateUSD
}
