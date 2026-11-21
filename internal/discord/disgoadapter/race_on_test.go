//go:build race

package disgoadapter

// disgo v0.20.0's gateway writes gatewayImpl.heartbeatCancel from the heartbeat
// goroutine and reads it in CloseWithCode without synchronisation (gateway.go:360
// vs :492), which the race detector flags. Tests that open a real gateway
// connection are skipped under -race until that is fixed upstream.
const raceEnabled = true
