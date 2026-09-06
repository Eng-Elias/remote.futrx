package kimi

import (
	"context"
	"encoding/json"
	"strings"
)

// Native cron jobs keep the session running between model turns. Their
// lifecycle is recorded in tool results and cron.fired notifications. Mirror
// IDs (never prompts) in session metadata so Remote can retain that behavior
// when resuming its own sessions.
func (r *serverRun) cronTool(t *childTool) {
	if t.IsError {
		return
	}
	switch t.Name {
	case "CronCreate":
		for _, job := range parseCronJobs(t.Output) {
			r.cronJobs[job.id] = job.recurring
			r.cronDirty = true
		}
	case "CronDelete":
		var args struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(t.Input, &args) == nil && args.ID != "" {
			delete(r.cronJobs, args.ID)
			r.cronDirty = true
		}
	case "CronList":
		if strings.HasPrefix(t.Output, "cron_jobs:") {
			r.cronJobs = map[string]bool{}
			for _, job := range parseCronJobs(t.Output) {
				r.cronJobs[job.id] = job.recurring
			}
			r.cronDirty = true
		}
	}
}

type cronJob struct {
	id        string
	recurring bool
}

func parseCronJobs(output string) []cronJob {
	var jobs []cronJob
	for _, line := range strings.Split(output, "\n") {
		if id, ok := strings.CutPrefix(line, "id: "); ok && id != "" {
			jobs = append(jobs, cronJob{id: id, recurring: true})
		}
		if len(jobs) > 0 && line == "recurring: false" {
			jobs[len(jobs)-1].recurring = false
		}
	}
	return jobs
}
func (r *serverRun) saveCron(ctx context.Context, p *serverTransport) error {
	if !r.cronDirty {
		return nil
	}
	if err := p.api(ctx, "POST", r.path()+"/profile", nativeProfileUpdate{Metadata: &nativeCronMetadata{Jobs: r.cronJobs}}, nil); err != nil {
		return err
	}
	r.cronDirty = false
	return nil
}
