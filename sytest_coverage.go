// go run ./sytest_coverage.go
// Add -v to see verbose output.

package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"sort"
	"strings"
)

// to generate test.list, clone sytest then:
// $ grep -r 'test "' ./tests > test.list

var (
	testNameRegexp      = regexp.MustCompile(`.*"(.*)"`)
	testFilenameRegexp  = regexp.MustCompile(`/tests/(.*)\.pl`)
	cleanFilenameRegexp = regexp.MustCompile(`[^a-zA-Z_]+`)
)

func main() {
	verbose := len(os.Args) == 2 && os.Args[1] == "-v"
	body, err := ioutil.ReadFile("./sytest.list")
	if err != nil {
		panic(err)
	}
	testLines := strings.Split(string(body), "\n")
	filenameToTestName := make(map[string][]string)
	for _, line := range testLines {
		name, filename := extract(line)
		if name == "" || filename == "" {
			continue
		}
		gof := goFilename(filename)
		filenameToTestName[gof] = append(filenameToTestName[gof], name)
	}
	numComplementTests := 0
	total := 0
	for _, fname := range sorted(filenameToTestName) {
		testNames := filenameToTestName[fname]
		// try to find the filename
		numTests := 0
		goCode, _ := ioutil.ReadFile("./tests/" + fname)
		if goCode != nil {
			for i := range testNames {
				// look for the sytest name contained in quotes
				if strings.Contains(string(goCode), `"`+testNames[i]+`"`) {
					numTests++
					testNames[i] = "✓ " + testNames[i]
				} else {
					testNames[i] = "× " + testNames[i]
				}
			}
		}
		fmt.Printf("%s %d/%d tests\n", fname, numTests, len(testNames))
		numComplementTests += numTests
		total += len(testNames)
		if numTests == 0 && !verbose {
			continue
		}
		for _, tn := range testNames {
			fmt.Printf("    %s\n", tn)
		}
		fmt.Println()
	}
	fmt.Printf("\nTOTAL: %d/%d tests converted\n", numComplementTests, total)
}

// 53groups/11publicise -> groups_publicise_test.go
// 10apidoc/31room-state -> apidoc_room_state_test.go
func goFilename(perlFilename string) string {
	perlFilename = strings.Replace(perlFilename, "/", "_", -1)
	perlFilename = strings.Replace(perlFilename, "-", "_", -1)
	perlFilename = strings.Replace(perlFilename, "/", "_", -1)
	perlFilename = strings.Replace(perlFilename, "/", "_", -1)
	return cleanFilenameRegexp.ReplaceAllString(perlFilename, "") + "_test.go"
}

func sorted(in map[string][]string) []string {
	out := make([]string, len(in))
	i := 0
	for k := range in {
		out[i] = k
		i++
	}
	sort.Strings(out)
	return out
}

// ./tests/49ignore.pl:test "Ignore invite in incremental sync",
// ./tests/31sync/16room-summary.pl:test "Room summary counts change when membership changes",
func extract(line string) (string, string) {
	line = strings.TrimSpace(line)
	if len(line) == 0 {
		return "", ""
	}
	nameGroups := testNameRegexp.FindStringSubmatch(line)
	filenameGroups := testFilenameRegexp.FindStringSubmatch(line)
	if nameGroups == nil {
		panic("Cannot find name: " + line)
	}
	if filenameGroups == nil {
		panic("Cannot find filename: " + line)
	}
	return nameGroups[1], filenameGroups[1]
}
