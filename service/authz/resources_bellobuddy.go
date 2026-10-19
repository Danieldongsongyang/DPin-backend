package authz

const ResourceBelloBuddyPlan = "bellobuddy_plan"
const ResourceBelloBuddyBinding = "bellobuddy_job_binding"
const ResourceBelloBuddyResume = "bellobuddy_resume_archive"

var (
	BelloBuddyPlanRead     = Permission{Resource: ResourceBelloBuddyPlan, Action: ActionRead}
	BelloBuddyPlanWrite    = Permission{Resource: ResourceBelloBuddyPlan, Action: ActionWrite}
	BelloBuddyBindingRead  = Permission{Resource: ResourceBelloBuddyBinding, Action: ActionRead}
	BelloBuddyBindingWrite = Permission{Resource: ResourceBelloBuddyBinding, Action: ActionWrite}
	BelloBuddyResumeRead   = Permission{Resource: ResourceBelloBuddyResume, Action: ActionRead}
	BelloBuddyResumeWrite  = Permission{Resource: ResourceBelloBuddyResume, Action: ActionWrite}
)

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceBelloBuddyResume, LabelKey: "BelloBuddy resume archives",
		Actions: []ActionDefinition{
			{Action: ActionRead, LabelKey: "Read resume archive status", DescriptionKey: "View archive tasks, errors and private storage usage.", DefaultRoles: []string{BuiltInRoleAdmin}},
			{Action: ActionWrite, LabelKey: "Abandon blocked resume archives", DescriptionKey: "Explicitly abandon a blocked task and delete its private PDF copy; preserve external receipts.", DefaultRoles: []string{BuiltInRoleAdmin}},
		},
	})
	RegisterResource(ResourceDefinition{
		Resource: ResourceBelloBuddyPlan, LabelKey: "BelloBuddy public plans",
		Actions: []ActionDefinition{
			{Action: ActionRead, LabelKey: "Read BelloBuddy public plans", DescriptionKey: "View public recruitment and communication plans, including disabled plans.", DefaultRoles: []string{BuiltInRoleAdmin}},
			{Action: ActionWrite, LabelKey: "Manage BelloBuddy public plans", DescriptionKey: "Create and update public recruitment and communication plans.", DefaultRoles: []string{BuiltInRoleAdmin}},
		},
	})
	RegisterResource(ResourceDefinition{
		Resource: ResourceBelloBuddyBinding, LabelKey: "BelloBuddy job bindings",
		Actions: []ActionDefinition{
			{Action: ActionRead, LabelKey: "Read BelloBuddy job bindings", DescriptionKey: "View recruitment job candidates and shared bot bindings.", DefaultRoles: []string{BuiltInRoleAdmin}},
			{Action: ActionWrite, LabelKey: "Manage BelloBuddy job bindings", DescriptionKey: "Confirm same-job mappings and configure shared bot channel references.", DefaultRoles: []string{BuiltInRoleAdmin}},
		},
	})
}
