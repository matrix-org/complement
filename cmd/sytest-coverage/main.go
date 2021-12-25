// go run ./sytest_coverage.go
// Add -v to see verbose output.

package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// to generate test.list, clone sytest then:
// $ grep -r 'test "' ./tests > test.list

var (
	testNameRegexp     = regexp.MustCompile(`.*"(.*)"`)
	testFilenameRegexp = regexp.MustCompile(`/tests/(.*)\.pl`)
)

// Maps test names to filenames then looks for:
//   sytest: $test_name
// in all files in ./tests - if there's a match it marks that test as converted.
func main() {
	verbose := len(os.Args) == 2 && os.Args[1] == "-v"

	filenameToTestName, testNameToFilename := getList()

	total := len(testNameToFilename)

	convertedTests := make(map[string]bool)

	// Walk all files defined under ./tests
	// we already panic inside Walk, so we can ignore the error
	_ = filepath.Walk("./tests", func(path string, info os.FileInfo, err error) error {
		// we don't care about directories or files not named "_test.go"
		if info.IsDir() || !strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		astFile, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		for _, cmt := range astFile.Comments {
			comment := strings.TrimSpace(cmt.Text())
			lines := strings.Split(comment, "\n")
			for _, line := range lines {
				_, ok := testNameToFilename[line]
				if !ok {
					continue
				}
				convertedTests[line] = true
			}
		}
		return nil
	})

	numComplementTests := len(convertedTests)
	for _, fname := range sorted(filenameToTestName) {
		testNames := filenameToTestName[fname]
		convertedTestsInFile := 0
		// try to find the filename
		for i := range testNames {
			// see if this test was converted
			if convertedTests[testNames[i]] {
				convertedTestsInFile++
				testNames[i] = "✓ " + strings.TrimPrefix(testNames[i], "sytest: ")
			} else {
				testNames[i] = "× " + strings.TrimPrefix(testNames[i], "sytest: ")
			}
		}
		fmt.Printf("%s %d/%d tests\n", fname, convertedTestsInFile, len(testNames))
		if !verbose || convertedTestsInFile == 0 {
			continue
		}
		for _, tn := range testNames {
			fmt.Printf("    %s\n", tn)
		}
		fmt.Println()
	}
	fmt.Printf("\nTOTAL: %d/%d tests converted\n", numComplementTests, total)
}

// filenameToTestName and testNameToFilename
// will filter ignored tests
func getList() (map[string][]string, map[string]string) {
	var ignoredTests = make(map[string]bool)
	var ignoredPaths []string
	ignoredBody, err := ioutil.ReadFile("./sytest.ignored.list")
	if err != nil {
		// ignore error, set body to nothing
		ignoredBody = []byte{}
	}
	ignoredLines := strings.Split(string(ignoredBody), "\n")
	for _, ignoredLine := range ignoredLines {
		ignoredLine = strings.TrimSpace(ignoredLine)

		if len(ignoredLine) == 0 || ignoredLine[0] == '#' {
			continue
		}

		if ignoredLine[0] == '!' {
			ignoredPaths = append(ignoredPaths, ignoredLine[1:])
		}

		ignoredTests[ignoredLine] = true
	}

	body, err := ioutil.ReadFile("./sytest.list")
	if err != nil {
		panic(err)
	}
	testLines := strings.Split(string(body), "\n")
	filenameToTestName := make(map[string][]string)
	testNameToFilename := make(map[string]string)
lines:
	for _, line := range testLines {
		name, filename := extract(line)
		if name == "" || filename == "" {
			continue
		}
		if _, ok := ignoredTests[name]; ok {
			continue
		}
		for _, path := range ignoredPaths {
			if strings.Contains(filename, path) {
				continue lines
			}
		}
		name = "sytest: " + strings.TrimSpace(name)
		filenameToTestName[filename] = append(filenameToTestName[filename], name)
		testNameToFilename[name] = strings.TrimSpace(filename)
	}

	return filenameToTestName, testNameToFilename
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
	if len(line) == 0 || line[0] == '#' {
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
