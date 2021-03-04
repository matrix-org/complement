package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/matrix-org/complement/cmd/account-snapshot/internal"
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
)

func main() {
	flag.Parse()
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
	snapshot := internal.Redact(syncData, anonMappings)
	snapshot.UserID = anonMappings.User(*flagUserID)
	bp, err := internal.ConvertToBlueprint(snapshot, "hs1")
	if err != nil {
		log.Panicf("FATAL: ConvertToBlueprint %s\n", err)
	}

	b, err := json.MarshalIndent(bp, "", "  ")
	if err != nil {
		log.Printf("WARNING: failed to marshal anonymous snapshot: %s", err)
	} else {
		fmt.Printf(string(b) + "\n")
	}
}
