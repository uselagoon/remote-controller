package harbor

import (
	"context"
	"fmt"
	"strings"

	harborclientv5model "github.com/mittwald/goharbor-client/v5/apiv2/model"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
)

// CreateProjectV2 will create a project if one doesn't exist, but will update as required.
func (h *Harbor) CreateProjectV2(ctx context.Context, namespace corev1.Namespace) (*harborclientv5model.Project, error) {
	projectName := namespace.Labels["lagoon.sh/project"]
	exists, err := h.ClientV5.ProjectExists(ctx, projectName)
	if err != nil {
		h.Log.Info(fmt.Sprintf("Error checking project %s exists, err: %v", projectName, err))
		return nil, err
	}
	if !exists {
		err := h.ClientV5.NewProject(ctx, &harborclientv5model.ProjectReq{
			ProjectName: projectName,
		})
		if err != nil {
			h.Log.Info(fmt.Sprintf("Error creating project %s, err: %v", projectName, err))
			return nil, err
		}
		project, err := h.ClientV5.GetProject(ctx, projectName)
		if err != nil {
			h.Log.Info(fmt.Sprintf("Error getting project %s, err: %v", projectName, err))
			return nil, err
		}
		stor := int64(-1)
		tStr := "true"
		project.Metadata = &harborclientv5model.ProjectMetadata{
			AutoScan:             &tStr,
			ReuseSysCVEAllowlist: &tStr,
			Public:               "false",
		}
		err = h.ClientV5.UpdateProject(ctx, project, &stor)
		if err != nil {
			h.Log.Info(fmt.Sprintf("Error updating project %s, err: %v", projectName, err))
			return nil, err
		}
	}
	project, err := h.ClientV5.GetProject(ctx, projectName)
	if err != nil {
		h.Log.Info(fmt.Sprintf("Error getting project %s, err: %v", projectName, err))
		return nil, err
	}

	// TODO: Repository support not required yet
	// this is a place holder
	// w, err := h.ClientV5.ListRepositories(ctx, int(project.ProjectID))
	// if err != nil {
	// 	return nil, err
	// }
	// for _, x := range w {
	// 	fmt.Println(x)
	// }

	return project, nil
}

// DeleteRepository will delete repositories related to an environment
func (h *Harbor) DeleteRepository(ctx context.Context, projectName, branch string) {
	environmentName := helpers.ShortenEnvironment(projectName, helpers.MakeSafe(branch))
	h.Config.PageSize = 100
	listRepositories := h.ListRepositories(ctx, projectName)
	for _, repo := range listRepositories {
		if strings.Contains(repo.Name, fmt.Sprintf("%s/%s", projectName, environmentName)) {
			repoName := strings.Replace(repo.Name, fmt.Sprintf("%s/", projectName), "", 1)
			err := h.ClientV5.DeleteRepository(ctx, projectName, repoName)
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error deleting harbor repository %s", repo.Name))
			}
			h.Log.Info(
				fmt.Sprintf(
					"Deleted harbor repository %s in  project %s, environment %s",
					repo.Name,
					projectName,
					environmentName,
				),
			)
		}
	}
	if len(listRepositories) > 100 {
		// h.Log.Info(fmt.Sprintf("more than pagesize repositories returned"))
		pageCount := int64(len(listRepositories) / 100)
		var page int64
		for page = 2; page <= pageCount; page++ {
			listRepositories := h.ListRepositories(ctx, projectName)
			for _, repo := range listRepositories {
				if strings.Contains(repo.Name, fmt.Sprintf("%s/%s", projectName, environmentName)) {
					repoName := strings.Replace(repo.Name, fmt.Sprintf("%s/", projectName), "", 1)
					err := h.ClientV5.DeleteRepository(ctx, projectName, repoName)
					if err != nil {
						h.Log.Info(fmt.Sprintf("Error deleting harbor repository %s", repo.Name))
					}
					h.Log.Info(
						fmt.Sprintf(
							"Deleted harbor repository %s in  project %s, environment %s",
							repo.Name,
							projectName,
							environmentName,
						),
					)
				}
			}
		}
	}
}

// ListRepositories .
func (h *Harbor) ListRepositories(ctx context.Context, projectName string) []*harborclientv5model.Repository {
	listRepositories, err := h.ClientV5.ListRepositories(ctx, projectName)
	if err != nil {
		h.Log.Info(fmt.Sprintf("Error listing harbor repositories for project %s", projectName))
	}
	return listRepositories
}
