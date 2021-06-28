package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/matrix-org/complement/cmd/account-snapshot/internal"
	"github.com/matrix-org/complement/internal/b"
)

/*
 * Account Snapshot - Take an anonymised snapshot of this account.
 * Raw /sync results stored in sync_snapshot.json
 * The anonymised output is written to stdout
 */

var (
	flagAccessToken = flag.String("token", "", "Account access token")
	flagHSURL       = flag.String("url", "https://matrix.org", "HS URL")
	flagUserID      = flag.String("user", "", "Matrix User ID, needed to configure blueprints correctly for account data")
	flagFromAnon    = flag.String("from-anon", "", "If set, loads anonymous snapshot from file and then produces blueprint")
	flagAnonOnly    = flag.Bool("anon-only", false, "If set, outputs an anonymous sync output only, not a blueprint")
	imageURI        = "complement-dendrite:latest"
)

func main() {
	flag.Parse()
	var snapshot *internal.Snapshot
	if *flagFromAnon != "" {
		f, err := os.Open(*flagFromAnon)
		if err != nil {
			log.Panicf("FATAL: Failed to open anonymous snapshot: %s", err)
		}
		if err := json.NewDecoder(f).Decode(&snapshot); err != nil {
			log.Panicf("FATAL: Failed to read anonymous snapshot as JSON: %s", err)
		}
	} else {
		if *flagAccessToken == "" || *flagUserID == "" {
			var eventsHandled string
			for evType := range internal.RedactRules {
				eventsHandled += "  " + evType + "\n"
			}
			fmt.Fprintf(os.Stderr,
				"Capture an anonymous snapshot of this account.\n"+
					"User name is required to map DM rooms correctly.\n"+
					"/sync output is stored in 'sync_snapshot.json'\n"+
					"Anonymised output is written to stdout\n\n"+
					"Usage: ./account_snapshot -token MDA.... -user @alice:matrix.org > output.json\n\n"+
					"Currently handles the following events:\n"+eventsHandled+"\n\n")
			flag.PrintDefaults()
			os.Exit(1)
		}

		syncData, err := internal.LoadSyncData(*flagHSURL, *flagAccessToken, "sync_snapshot.json")
		if err != nil {
			log.Panicf("FATAL: LoadSyncData %s\n", err)
		}
		var anonMappings internal.AnonMappings
		anonMappings.Devices = make(map[string]string)
		anonMappings.Servers = make(map[string]string)
		anonMappings.Users = make(map[string]string)
		anonMappings.Rooms = make(map[string]string)
		anonMappings.AnonUserToDevices = make(map[string]map[string]bool)
		anonMappings.SingleServerName = "hs1"
		snapshot = internal.Redact(syncData, anonMappings)
		snapshot.UserID = anonMappings.User(*flagUserID)
		if *flagAnonOnly {
			b, err := json.MarshalIndent(snapshot, "", "  ")
			if err != nil {
				log.Printf("WARNING: failed to marshal anonymous snapshot: %s", err)
			} else {
				fmt.Printf(string(b) + "\n")
			}
			os.Exit(0)
		}
	}
	bp, err := internal.ConvertToBlueprint(snapshot, "hs1")
	if err != nil {
		log.Panicf("FATAL: ConvertToBlueprint %s\n", err)
	}

	homerunnerReq := struct {
		Blueprint    *b.Blueprint `json:"blueprint"`
		BaseImageURI string       `json:"base_image_uri"`
	}{
		Blueprint:    bp,
		BaseImageURI: imageURI,
	}

	b, err := json.MarshalIndent(homerunnerReq, "", "  ")
	if err != nil {
		log.Printf("WARNING: failed to marshal blueprint: %s", err)
	} else {
		fmt.Printf(string(b) + "\n")
	}
}
