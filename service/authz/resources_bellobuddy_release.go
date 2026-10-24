package authz

const (
	ResourceBelloBuddyRelease = "bellobuddy_release"
	ActionPublish             = "publish"
	ActionWithdraw            = "withdraw"
)

var (
	BelloBuddyReleaseRead     = Permission{Resource: ResourceBelloBuddyRelease, Action: ActionRead}
	BelloBuddyReleaseWrite    = Permission{Resource: ResourceBelloBuddyRelease, Action: ActionWrite}
	BelloBuddyReleasePublish  = Permission{Resource: ResourceBelloBuddyRelease, Action: ActionPublish}
	BelloBuddyReleaseWithdraw = Permission{Resource: ResourceBelloBuddyRelease, Action: ActionWithdraw}
)

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceBelloBuddyRelease, LabelKey: "BelloBuddy releases",
		Actions: []ActionDefinition{
			{Action: ActionRead, LabelKey: "Read BelloBuddy releases", DescriptionKey: "View desktop release drafts, artifacts and publication history."},
			{Action: ActionWrite, LabelKey: "Manage BelloBuddy releases", DescriptionKey: "Create drafts, upload artifacts and record acceptance evidence."},
			{Action: ActionPublish, LabelKey: "Publish BelloBuddy releases", DescriptionKey: "Recommend a validated desktop release to the stable channel."},
			{Action: ActionWithdraw, LabelKey: "Withdraw BelloBuddy releases", DescriptionKey: "Stop recommending or urgently disable a desktop release."},
		},
	})
}
