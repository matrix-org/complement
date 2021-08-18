// go run ./sytest_coverage.go
// Add -v to see verbose output.

package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
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
	body, err := ioutil.ReadFile("./sytest.list")
	if err != nil {
		panic(err)
	}
	testLines := strings.Split(string(body), "\n")
	filenameToTestName := make(map[string][]string)
	testNameToFilename := make(map[string]string)
	for _, line := range testLines {
		name, filename := extract(line)
		if name == "" || filename == "" {
			continue
		}
		name = "sytest: " + strings.TrimSpace(name)
		filenameToTestName[filename] = append(filenameToTestName[filename], name)
		testNameToFilename[name] = strings.TrimSpace(filename)
	}
	total := len(testNameToFilename)
	files, err := ioutil.ReadDir("./tests")
	if err != nil {
		panic(err)
	}
	convertedTests := make(map[string]bool)

	countConvertedTests(files, convertedTests, testNameToFilename, "./tests/")
	files, err = ioutil.ReadDir("./tests/csapi")
	if err != nil {
		panic(err)
	}
	countConvertedTests(files, convertedTests, testNameToFilename, "./tests/csapi/")

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

func countConvertedTests(files []os.FileInfo, convertedTests map[string]bool, testNameToFilename map[string]string, path string) {
	for _, file := range files {
		fset := token.NewFileSet()
		if file.Name() == "csapi" {
			continue
		}
		astFile, err := parser.ParseFile(fset, path+file.Name(), nil, parser.ParseComments)
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
	}
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
