package messenger

import (
	"context"
	"encoding/json"
	"fmt"

	harborclientv5model "github.com/mittwald/goharbor-client/v5/apiv2/model"
	"github.com/uselagoon/machinery/utils/cron"
	lagoonv1beta2 "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/harbor"
	ctrl "sigs.k8s.io/controller-runtime"
)

type RetentionEvent struct {
	Type      string `json:"type"`      // defines the action type
	EventType string `json:"eventType"` // defines the eventtype field in the event notification
	Data      Data   `json:"data"`      // contains the payload for the action, this could be any json so using a map
}

type Data struct {
	Project Project               `json:"project"`
	Policy  HarborRetentionPolicy `json:"policy"`
}

type Project struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type HarborRetentionPolicy struct {
	Enabled  bool                  `json:"enabled"`
	Rules    []HarborRetentionRule `json:"rules"`
	Schedule string                `json:"schedule"`
}

type HarborRetentionRule struct {
	Name         string `json:"name"`
	Pattern      string `json:"pattern"`
	LatestPulled uint64 `json:"latestPulled"`
}

// HarborPolicy handles harbor retention policy changes.
func (m *Messenger) HarborPolicy(ctx context.Context, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	retPol := &RetentionEvent{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, retPol); err != nil {
		return err
	}
	lagoonHarbor, err := harbor.New(m.Harbor)
	if err != nil {
		return err
	}
	projectName := retPol.Data.Project.Name
	project, err := lagoonHarbor.ClientV5.GetProject(ctx, projectName)
	if err != nil {
		opLog.Info(fmt.Sprintf("Error getting project %s, err: %v", projectName, err))
		return err
	}
	// handle the creation and updating of retention policies as required
	// create the retention policy as required
	var retentionPolicy *harborclientv5model.RetentionPolicy
	switch retPol.EventType {
	case "updatePolicy":
		opLog.Info(
			fmt.Sprintf(
				"Received harbor policy update for project %s",
				projectName,
			),
		)
		// if this is updating or adding a policy, handle that here
		retentionPolicy, err = m.generateRetentionPolicy(int64(project.ProjectID), projectName, retPol.Data.Policy)
		if err != nil {
			opLog.Info(fmt.Sprintf("Error generating retention policy for project %s, err: %v", projectName, err))
			return err
		}
	case "removePolicy":
		opLog.Info(
			fmt.Sprintf(
				"Received harbor policy removal for project %s",
				projectName,
			),
		)
		// add an empty retention policy
		retentionPolicy = m.generateEmptyRetentionPolicy(int64(project.ProjectID))
	default:
		return fmt.Errorf("unable to determine policy type")
	}
	// get the existing one if one exists
	existingPolicy, err := lagoonHarbor.ClientV5.GetRetentionPolicyByProject(ctx, projectName)
	if err != nil {
		opLog.Info(fmt.Sprintf("Error getting retention policy %s: %v", project.Name, err))
		return err
	}
	if existingPolicy != nil {
		retentionPolicy.ID = existingPolicy.ID
		r1, _ := json.Marshal(existingPolicy)
		r2, _ := json.Marshal(retentionPolicy)
		// if the policy differs, then we need to update it with our new policy
		if string(r1) != string(r2) {
			err := lagoonHarbor.ClientV5.UpdateRetentionPolicy(ctx, retentionPolicy)
			if err != nil {
				f, _ := json.Marshal(err)
				opLog.Info(fmt.Sprintf("Error updating retention policy %s: %v", project.Name, string(f)))
				return err
			}
		}
	} else {
		// create it if it doesn't
		if err := lagoonHarbor.ClientV5.NewRetentionPolicy(ctx, retentionPolicy); err != nil {
			opLog.Info(fmt.Sprintf("Error creating retention policy %s: %v", project.Name, err))
			return err
		}
	}
	return nil
}

func (m *Messenger) generateRetentionPolicy(projectID int64, projectName string, policy HarborRetentionPolicy) (*harborclientv5model.RetentionPolicy, error) {
	// generate a somewhat random schedule from the retention schedule template, using the harbor projectname as the seed
	schedule, err := cron.ConvertCrontab(projectName, policy.Schedule)
	if err != nil {
		return nil, fmt.Errorf("error generating retention schedule %s: %v", projectName, err)
	}
	retPol := &harborclientv5model.RetentionPolicy{
		Algorithm: "or",
		Scope: &harborclientv5model.RetentionPolicyScope{
			Level: "project",
			Ref:   projectID,
		},
		Rules: []*harborclientv5model.RetentionRule{},
		Trigger: &harborclientv5model.RetentionRuleTrigger{
			Kind: "Schedule",
			Settings: map[string]string{
				"cron": fmt.Sprintf("0 %s", schedule), // harbor needs seconds :\ just add a 0 pad to the start of the schedule
			},
		},
	}
	for _, rule := range policy.Rules {
		retPol.Rules = append(retPol.Rules,
			&harborclientv5model.RetentionRule{
				Action: "retain",
				Params: map[string]interface{}{
					"latestPulledN": rule.LatestPulled,
				},
				ScopeSelectors: map[string][]harborclientv5model.RetentionSelector{
					"repository": {
						{
							Decoration: "repoMatches",
							Kind:       "doublestar",
							Pattern:    rule.Pattern,
						},
					},
				},
				TagSelectors: []*harborclientv5model.RetentionSelector{
					{
						Decoration: "matches",
						Extras:     "{\"untagged\":true}",
						Kind:       "doublestar",
						Pattern:    "**",
					},
				},
				Template: "latestPulledN",
			},
		)
	}
	return retPol, nil
}

func (m *Messenger) generateEmptyRetentionPolicy(projectID int64) *harborclientv5model.RetentionPolicy {
	return &harborclientv5model.RetentionPolicy{
		Algorithm: "or",
		Rules:     []*harborclientv5model.RetentionRule{},
		Scope: &harborclientv5model.RetentionPolicyScope{
			Level: "project",
			Ref:   projectID,
		},
		Trigger: &harborclientv5model.RetentionRuleTrigger{
			Kind: "Schedule",
			Settings: map[string]string{
				"cron": "",
			},
		}}
}
