package run

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/leg100/pug/internal/module"
	"github.com/leg100/pug/internal/resource"
	"github.com/leg100/pug/internal/workspace"
)

type Status string

const (
	Pending            Status = "pending"
	Scheduled          Status = "scheduled"
	PlanQueued         Status = "plan queued"
	Planning           Status = "planning"
	Planned            Status = "planned"
	PlannedAndFinished Status = "planned&finished"
	ApplyQueued        Status = "apply queued"
	Applying           Status = "applying"
	Applied            Status = "applied"
	Errored            Status = "errored"
	Canceled           Status = "canceled"
	Discarded          Status = "discarded"

	MaxStatusLen = len(PlannedAndFinished)
)

type Run struct {
	resource.Resource

	Created time.Time
	Updated time.Time

	Status    Status
	AutoApply bool
	PlanOnly  bool

	PlanReport  Report
	ApplyReport Report

	// TODO: are these needed?
	PlanTask  *resource.Resource
	ApplyTask *resource.Resource

	// Error is non-nil when the run status is Errored
	Error error

	// Call this function after every status update
	afterUpdate func(run *Run)
}

type CreateOptions struct {
	PlanOnly bool

	afterUpdate func(run *Run)
}

func newRun(mod *module.Module, ws *workspace.Workspace, opts CreateOptions) (*Run, error) {
	run := &Run{
		Resource:    resource.New(resource.Run, "", &ws.Resource),
		Status:      Pending,
		AutoApply:   ws.AutoApply,
		Created:     time.Now(),
		Updated:     time.Now(),
		afterUpdate: opts.afterUpdate,
	}

	// create a dedicated pug directory for the run, in which the plan file
	// goes, etc.
	pugdir := filepath.Join(run.ModulePath(), run.PugDirectory())
	if err := os.MkdirAll(pugdir, 0o755); err != nil {
		return nil, err
	}

	return run, nil
}

func (r *Run) Workspace() resource.Resource {
	return *r.Parent
}

func (r *Run) WorkspaceName() string {
	return r.Parent.String()
}

func (r *Run) Module() resource.Resource {
	return *r.Workspace().Parent
}

func (r *Run) ModulePath() string {
	return r.Module().String()
}

// PugDirectory is the run's pug directory, relative to the module's directory.
func (r *Run) PugDirectory() string {
	return filepath.Join(workspace.PugDirectory(r.Workspace().String()), r.ID().String())
}

// PlanPath is the path to the run's plan file, relative to the module's
// directory.
func (r *Run) PlanPath() string {
	return filepath.Join(r.PugDirectory(), "plan.out")
}

func (r *Run) IsFinished() bool {
	switch r.Status {
	case PlannedAndFinished, Applied, Errored, Canceled:
		return true
	default:
		return false
	}
}

func (r *Run) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", r.ID().String()),
		slog.String("workspace", r.WorkspaceName()),
		slog.String("module", r.ModulePath()),
	)
}

func (r *Run) setErrored(err error) {
	r.Error = err
	r.updateStatus(Errored)
}

func (r *Run) updateStatus(status Status) {
	r.Status = status
	r.Updated = time.Now()
	if r.afterUpdate != nil {
		r.afterUpdate(r)
	}
	if r.IsFinished() {
		slog.Info("completed run", "status", r.Status, "run", r)
	}
}
