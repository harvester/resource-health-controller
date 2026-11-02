package harvesterhci

import "github.com/rancher/wrangler/v3/pkg/condition"

// The volume remote operators only have a Ready condition: it is false both while an operation runs and after it fails.
// The two are told apart by the message, which is the error text only for a failure.
const volumeRemoteReady condition.Cond = "Ready"

const volumeRemoteInProgressMessage = "Operation in progress"
