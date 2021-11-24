// +build dendrite_blacklist

package runtime

func init() {
	Homeserver = Dendrite
}
