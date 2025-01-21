//go:build conduwuit_blacklist || conduit_blacklist
// +build conduwuit_blacklist conduit_blacklist

// for now, a couple skipped conduit tests still apply to conduwuit. this will change in the future

package runtime

func init() {
	Homeserver = Conduwuit
}
