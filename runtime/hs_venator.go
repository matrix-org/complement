//go:build venator_blacklist

package runtime

func init() {
	Homeserver = Venator
}
