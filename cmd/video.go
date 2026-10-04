package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/iamnikolie/gengoya/internal/config"
	"github.com/iamnikolie/gengoya/internal/jobs"
	"github.com/iamnikolie/gengoya/internal/provider"
	"github.com/iamnikolie/gengoya/internal/registry"
	"github.com/spf13/cobra"
)

var (
	videoImagePath     string
	videoLastFramePath string
	videoRefPaths      []string
	videoEdit          string
	videoDelivery      string
	videoNegative      string
	videoResolution    string
	videoDuration      string
	videoPerson        string
	videoDetach        bool
	videoPollInterval  time.Duration
	videoTimeout       time.Duration
	posterFrames       int
)

var videoCmd = &cobra.Command{
	Use:   "video [prompt]",
	Short: "Generate a video from a text prompt (and optional first frame)",
	Long: `Generate a video with Veo or Gemini Omni (Gemini). The job is long-running: by default
gengoya waits for it, downloads the clip, and prints its path.

With --detach the operation name is printed instead and the job is recorded;
collect it later with 'gengoya jobs fetch <id>'.

Because an agent cannot Read an mp4, a PNG contact sheet of --poster-frames
frames is written next to the clip (needs ffmpeg on PATH) and its path is
printed after the video path.`,
	Args:        cobra.MaximumNArgs(1),
	Annotations: map[string]string{"kind": "video"},
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := ""
		if len(args) == 1 {
			prompt = args[0]
		}
		if prompt == "" && videoImagePath == "" {
			return fmt.Errorf("a prompt or --image is required")
		}
		if videoEdit != "" && prompt == "" {
			return fmt.Errorf("--edit needs a prompt describing the change")
		}
		if nImages != 1 {
			return fmt.Errorf("video generation returns one clip per request; -n must be 1")
		}
		if !registry.SupportsOp(sel.Model, "video") {
			return fmt.Errorf("model %q does not support video", sel.Alias)
		}

		res, dur, aspect, err := resolveVideoParams(sel.Model, videoResolution, videoDuration, aspectFlag, len(videoRefPaths))
		if err != nil {
			return err
		}

		req := provider.VideoRequest{
			Model:            sel.Model,
			Prompt:           prompt,
			NegativePrompt:   videoNegative,
			Resolution:       res,
			Aspect:           aspect,
			Duration:         dur,
			PersonGeneration: videoPerson,
			Detach:           videoDetach,
			Delivery:         videoDelivery,
		}
		if videoImagePath != "" {
			b, err := os.ReadFile(videoImagePath)
			if err != nil {
				return fmt.Errorf("read --image %s: %w", videoImagePath, err)
			}
			req.Image, req.ImageName = b, videoImagePath
		}
		if videoEdit != "" {
			if !provider.IsOmniModel(sel.Model.APIID) {
				return fmt.Errorf("--edit is supported by Omni models only (e.g. --model omni-flash)")
			}
			if err := resolveEditSource(&req, videoEdit); err != nil {
				return err
			}
		}
		if videoLastFramePath != "" {
			b, err := os.ReadFile(videoLastFramePath)
			if err != nil {
				return fmt.Errorf("read --last-frame %s: %w", videoLastFramePath, err)
			}
			req.LastFrame, req.LastFrameName = b, videoLastFramePath
		}
		for _, p := range videoRefPaths {
			b, err := os.ReadFile(p)
			if err != nil {
				return fmt.Errorf("read --ref %s: %w", p, err)
			}
			req.RefImages = append(req.RefImages, b)
			req.RefImageNames = append(req.RefImageNames, p)
		}

		// Build the request body now (pure, no network) so a refused flag
		// combination fails before the cost prompt, not after the user agreed.
		build := provider.BuildVeoRequest
		if provider.IsOmniModel(sel.Model.APIID) {
			build = provider.BuildOmniRequest
		} else if videoDelivery != "" {
			return fmt.Errorf("--delivery is supported by Omni models only (e.g. --model omni-flash)")
		}
		if _, err := build(req); err != nil {
			return err
		}

		est := registry.EstimateVideoCost(sel.Model, res, dur)
		if err := confirmCostThreshold(est, videoCostThreshold, assumeYes, os.Stdout, os.Stdin, stderr); err != nil {
			return err
		}

		profileDir, err := config.ProfileDir(profile)
		if err != nil {
			return err
		}

		start := time.Now()
		operation, err := videoProv.Start(cmd.Context(), req)
		if err != nil {
			return err
		}

		job := jobs.Job{
			ID:              jobs.IDForOperation(operation),
			Operation:       operation,
			Provider:        sel.Provider,
			Model:           sel.Alias,
			APIID:           sel.Model.APIID,
			Prompt:          prompt,
			Resolution:      res,
			Duration:        dur,
			Aspect:          aspect,
			OutDir:          outDir,
			Name:            nameBase,
			PosterFrames:    posterFrames,
			CostEstimateUSD: est,
			CreatedAt:       time.Now(),
			State:           jobs.StatePending,
		}
		if err := jobs.Save(profileDir, job); err != nil {
			return err
		}

		if videoDetach {
			fmt.Fprintf(stderr, "model=%s id=%s res=%s dur=%ss aspect=%s %s job=%s detached\n",
				sel.Alias, sel.Model.APIID, res, dur, aspect,
				costLabel(est), job.ID)
			if jsonOutput {
				return emitVideoResult(nil, videoMetaFor(job, jobs.StatePending, time.Since(start)), true, os.Stdout, stderr)
			}
			fmt.Fprintln(os.Stdout, operation)
			return nil
		}

		ctx, cancel := contextWithTimeout(cmd.Context(), videoTimeout)
		defer cancel()

		arts, job, err := waitFetchWrite(ctx, videoProv, job, profileDir)
		if err != nil {
			return err
		}
		return emitVideoResult(arts, videoMetaFor(job, jobs.StateFetched, time.Since(start)), jsonOutput, os.Stdout, stderr)
	},
}

// videoMetaFor renders a job record as the --json payload / status line source.
func videoMetaFor(job jobs.Job, state string, elapsed time.Duration) jsonVideoResult {
	src := provider.CostSourceRegistry
	cost := job.CostEstimateUSD
	if job.CostSource != "" {
		src = job.CostSource
		cost = job.CostUSD
	}
	return jsonVideoResult{
		Model: job.Model, APIID: job.APIID, Provider: job.Provider,
		Resolution: job.Resolution, DurationSeconds: job.Duration, Aspect: job.Aspect,
		Operation: job.Operation, JobID: job.ID, State: state,
		CostEstimateUSD: cost,
		CostSource:      src,
		ElapsedMS:       elapsed.Milliseconds(),
	}
}

// resolveVideoParams fills resolution/duration/aspect from flags, model
// defaults and registry cross-field constraints.
func resolveVideoParams(m registry.Model, resFlag, durFlag, aspect string, refCount int) (res, dur, asp string, err error) {
	res = resFlag
	if res == "" {
		res = registry.DefaultResolution(m)
	}
	if err := registry.ValidateResolution(m, res); err != nil {
		return "", "", "", err
	}
	if err := registry.ValidateDuration(m, durFlag); err != nil {
		return "", "", "", err
	}
	if err := registry.ValidateVideoCombo(m, res, durFlag, refCount); err != nil {
		return "", "", "", err
	}
	dur = registry.ResolveVideoDuration(m, res, durFlag, refCount)
	if err := registry.ValidateDuration(m, dur); err != nil {
		return "", "", "", err
	}
	asp = aspect
	if asp == "" {
		asp = defaultVideoAspect(m)
	}
	if err := registry.ValidateAspect(m, asp); err != nil {
		return "", "", "", err
	}
	return res, dur, asp, nil
}

func defaultVideoAspect(m registry.Model) string {
	if len(m.AspectRatios) > 0 {
		return m.AspectRatios[0]
	}
	return ""
}

func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, d)
}

// waitFetchWrite polls the job's operation to completion, downloads every clip,
// writes it (plus poster) and updates the stored job record. It returns the
// updated record so callers see the final state.
func waitFetchWrite(ctx context.Context, vp provider.VideoProvider, job jobs.Job, profileDir string) ([]videoArtifact, jobs.Job, error) {
	op, err := vp.Wait(ctx, job.Operation, videoPollInterval, func(elapsed time.Duration, op provider.VideoOperation) {
		state := "running"
		if op.Done {
			state = "done"
		}
		fmt.Fprintf(stderr, "job=%s %s t=%ds\n", job.ID, state, int(elapsed.Seconds()))
	})
	if err != nil {
		job.State = jobs.StateFailed
		job.Error = err.Error()
		_ = jobs.Save(profileDir, job)
		return nil, job, err
	}

	job.State = jobs.StateDone
	if op.CostSource != "" {
		job.CostUSD, job.CostSource = op.CostUSD, op.CostSource
	}
	_ = jobs.Save(profileDir, job)

	videos := append([]provider.Video(nil), op.Videos...)
	for _, uri := range op.URIs {
		v, err := vp.Download(ctx, uri)
		if err != nil {
			job.State = jobs.StateFailed
			job.Error = err.Error()
			_ = jobs.Save(profileDir, job)
			return nil, job, err
		}
		videos = append(videos, v)
	}
	if len(videos) == 0 {
		return nil, job, fmt.Errorf("operation %s returned no videos", job.Operation)
	}

	durSecs, _ := strconv.ParseFloat(job.Duration, 64)
	out := job.OutDir
	if out == "" {
		out = "."
	}
	arts, err := writeVideos(ctx, out, job.Prompt, job.Name, videos, job.PosterFrames, durSecs, stderr)
	if err != nil {
		return nil, job, err
	}

	job.State = jobs.StateFetched
	job.Paths = artifactPaths(arts)
	_ = jobs.Save(profileDir, job)
	return arts, job, nil
}

// resolveEditSource fills the Omni edit source on req: an existing local file
// is sent inline as the video to edit/extend; anything else is taken as an
// interaction id (or job id / "interactions/<id>") for a stateful follow-up.
func resolveEditSource(req *provider.VideoRequest, src string) error {
	if st, err := os.Stat(src); err == nil && !st.IsDir() {
		b, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read --edit %s: %w", src, err)
		}
		const maxInline = 15 << 20
		if len(b) > maxInline {
			return fmt.Errorf("--edit %s is %d MB; inline upload is capped at 15 MB (Omni takes clips up to 10s)", src, len(b)>>20)
		}
		req.EditVideo, req.EditVideoName = b, src
		return nil
	}
	if profileDir, err := config.ProfileDir(profile); err == nil {
		if j, err := jobs.Resolve(profileDir, src); err == nil && strings.HasPrefix(j.Operation, provider.OperationPrefixOmni) {
			req.PreviousInteraction = j.Operation
			return nil
		}
	}
	if strings.ContainsAny(src, "/.\\") && !strings.HasPrefix(src, provider.OperationPrefixOmni) {
		return fmt.Errorf("--edit %q is neither an existing file nor an interaction id", src)
	}
	req.PreviousInteraction = src
	return nil
}

func init() {
	videoCmd.Flags().StringVar(&videoEdit, "edit", "", "Omni: edit/extend a video — a local mp4 (<=10s) or a prior interaction/job id")
	videoCmd.Flags().StringVar(&videoDelivery, "delivery", "", "Omni: inline|uri clip delivery (default: inline <=720p, uri above)")
	videoCmd.Flags().StringVar(&videoImagePath, "image", "", "first frame image (image-to-video)")
	videoCmd.Flags().StringVar(&videoLastFramePath, "last-frame", "", "final frame image (interpolation; requires --image)")
	videoCmd.Flags().StringArrayVar(&videoRefPaths, "ref", nil, "reference image for style/subject (repeatable)")
	videoCmd.Flags().StringVar(&videoNegative, "negative", "", "negative prompt")
	videoCmd.Flags().StringVar(&videoResolution, "resolution", "", "360p|720p|1080p|4k, per model (model default if omitted)")
	videoCmd.Flags().StringVar(&videoDuration, "duration", "", "clip length in seconds; per model (Veo 4|6|8; Omni see 'gengoya models')")
	videoCmd.Flags().StringVar(&videoPerson, "person", "", "personGeneration: allow_all|allow_adult (Veo only)")
	videoCmd.Flags().BoolVar(&videoDetach, "detach", false, "start the job, print the operation name, and exit")
	videoCmd.Flags().DurationVar(&videoPollInterval, "poll-interval", 10*time.Second, "operation poll interval")
	videoCmd.Flags().DurationVar(&videoTimeout, "timeout", 15*time.Minute, "give up waiting after this long (0 = no limit)")
	videoCmd.Flags().IntVar(&posterFrames, "poster-frames", 4, "PNG contact-sheet frames written next to the clip (0 = off)")
	rootCmd.AddCommand(videoCmd)
}
