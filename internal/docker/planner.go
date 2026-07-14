package docker

import "dockertree/internal/core"

func PreviewUpdate(project core.Project, removeOrphans bool) core.UpdatePlan {
	plan := core.UpdatePlan{ProjectID: project.ID, ProjectName: project.Name, WorkingDir: project.WorkingDir}
	for _, svc := range project.Services {
		plan.Services = append(plan.Services, svc.Name)
	}
	if project.Type == core.ProjectTypeStandalone {
		plan.Warnings = append(plan.Warnings, "Standalone containers cannot be safely recreated in v1 because original docker run options are not reliably recoverable. Migrate this container to Compose before using one-click deploy.")
		return plan
	}
	if len(project.ConfigFiles) == 0 {
		plan.Warnings = append(plan.Warnings, "No compose file is known for this project. Run scan again or add a scan path.")
		return plan
	}
	for _, cmd := range UpdateCommands(project, removeOrphans) {
		plan.Commands = append(plan.Commands, cmd.String())
	}
	plan.CanDeploy = true
	return plan
}
