package workflow

import (
	"context"

	"github.com/go-gorp/gorp"

	"github.com/ovh/cds/engine/api/application"
	"github.com/ovh/cds/engine/api/cache"
	"github.com/ovh/cds/engine/api/environment"
	"github.com/ovh/cds/engine/api/observability"
	"github.com/ovh/cds/sdk"
	"github.com/ovh/cds/sdk/exportentities"
	v2 "github.com/ovh/cds/sdk/exportentities/v2"
)

// Export a workflow
func Export(ctx context.Context, db gorp.SqlExecutor, cache cache.Store, proj sdk.Project, name string, opts ...v2.ExportOptions) (exportentities.Workflow, error) {
	ctx, end := observability.Span(ctx, "workflow.Export")
	defer end()

	wf, errload := Load(ctx, db, cache, proj, name, LoadOptions{})
	if errload != nil {
		return v2.Workflow{}, sdk.WrapError(errload, "workflow.Export> Cannot load workflow %s", name)
	}

	// If repo is from as-code do not export WorkflowSkipIfOnlyOneRepoWebhook
	if wf.FromRepository != "" {
		opts = append(opts, v2.WorkflowSkipIfOnlyOneRepoWebhook)
	}

	wkf, err := exportentities.NewWorkflow(ctx, *wf, opts...)
	if err != nil {
		return v2.Workflow{}, sdk.WrapError(err, "unable to export workflow")
	}

	return wkf, nil
}

// Pull a workflow with all it dependencies; it writes a tar buffer in the writer
func Pull(ctx context.Context, db gorp.SqlExecutor, cache cache.Store, proj sdk.Project, name string,
	encryptFunc sdk.EncryptFunc, opts ...v2.ExportOptions) (exportentities.WorkflowComponents, error) {

	ctx, end := observability.Span(ctx, "workflow.Pull")
	defer end()

	var wp exportentities.WorkflowComponents

	wf, err := Load(ctx, db, cache, proj, name, LoadOptions{
		DeepPipeline: true,
	})
	if err != nil {
		return wp, sdk.WrapError(err, "cannot load workflow %s", name)
	}

	// Reload app to retrieve secrets
	for i := range wf.Applications {
		app := wf.Applications[i]
		vars, err := application.GetAllVariable(db, proj.Key, app.Name, application.WithClearPassword())
		if err != nil {
			return wp, sdk.WrapError(err, "cannot load application variables %s", app.Name)
		}
		app.Variable = vars

		keys, err := application.LoadAllKeysWithPrivateContent(db, app.ID)
		if err != nil {
			return wp, sdk.WrapError(err, "cannot load application keys %s", app.Name)
		}
		app.Keys = keys

		wf.Applications[i] = app
	}

	// Reload env to retrieve secrets
	for i := range wf.Environments {
		env := wf.Environments[i]
		vars, err := environment.GetAllVariable(db, proj.Key, env.Name, environment.WithClearPassword())
		if err != nil {
			return wp, sdk.WrapError(err, "cannot load environment variables %s", env.Name)
		}
		env.Variable = vars

		if err := environment.LoadAllDecryptedKeys(ctx, db, &env); err != nil {
			return wp, sdk.WrapError(err, "cannot load environment keys %s", env.Name)
		}
		wf.Environments[i] = env
	}

	// If the repository is "as-code", hide the hook
	if wf.FromRepository != "" {
		opts = append(opts, v2.WorkflowSkipIfOnlyOneRepoWebhook)
	}
	wp.Workflow, err = exportentities.NewWorkflow(ctx, *wf, opts...)
	if err != nil {
		return wp, sdk.WrapError(err, "unable to export workflow")
	}

	for _, a := range wf.Applications {
		if a.FromRepository != wf.FromRepository { // don't export if coming from an other repository
			continue
		}
		app, err := application.ExportApplication(db, a, encryptFunc)
		if err != nil {
			return wp, sdk.WrapError(err, "unable to export app %s", a.Name)
		}
		wp.Applications = append(wp.Applications, app)
	}

	for _, e := range wf.Environments {
		if e.FromRepository != wf.FromRepository { // don't export if coming from an other repository
			continue
		}
		env, err := environment.ExportEnvironment(db, e, encryptFunc)
		if err != nil {
			return wp, sdk.WrapError(err, "unable to export env %s", e.Name)
		}
		wp.Environments = append(wp.Environments, env)
	}

	for _, p := range wf.Pipelines {
		if p.FromRepository != wf.FromRepository { // don't export if coming from an other repository
			continue
		}
		wp.Pipelines = append(wp.Pipelines, exportentities.NewPipelineV1(p))
	}

	return wp, nil
}
