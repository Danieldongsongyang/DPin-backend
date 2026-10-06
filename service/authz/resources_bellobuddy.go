package authz

const ResourceBelloBuddyPlan = "bellobuddy_plan"
const ResourceBelloBuddyBinding = "bellobuddy_job_binding"

var (
	BelloBuddyPlanRead     = Permission{Resource: ResourceBelloBuddyPlan, Action: ActionRead}
	BelloBuddyPlanWrite    = Permission{Resource: ResourceBelloBuddyPlan, Action: ActionWrite}
	BelloBuddyBindingRead  = Permission{Resource: ResourceBelloBuddyBinding, Action: ActionRead}
	BelloBuddyBindingWrite = Permission{Resource: ResourceBelloBuddyBinding, Action: ActionWrite}
)

func init() {
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
