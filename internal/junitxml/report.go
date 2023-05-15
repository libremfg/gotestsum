/*Package junitxml creates a JUnit XML report from a testjson.Execution.
 */
package junitxml

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"gotest.tools/gotestsum/internal/log"
	"gotest.tools/gotestsum/testjson"
)

// JUnitTestSuites is a collection of JUnit test suites.
type JUnitTestSuites struct {
	XMLName  xml.Name `xml:"testsuites"`
	Name     string   `xml:"name,attr,omitempty"`
	Tests    int      `xml:"tests,attr"`
	Failures int      `xml:"failures,attr"`
	Errors   int      `xml:"errors,attr"`
	Time     string   `xml:"time,attr"`
	Suites   []JUnitTestSuite
}

// JUnitTestSuite is a single JUnit test suite which may contain many
// testcases.
type JUnitTestSuite struct {
	XMLName    xml.Name        `xml:"testsuite"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Time       string          `xml:"time,attr"`
	Name       string          `xml:"name,attr"`
	Properties JUnitProperties `xml:"properties,omitempty"`
	TestCases  []JUnitTestCase
	Timestamp  string `xml:"timestamp,attr"`
}

// JUnitTestCase is a single test case with its result.
type JUnitTestCase struct {
	XMLName     xml.Name          `xml:"testcase"`
	Classname   string            `xml:"classname,attr"`
	Name        string            `xml:"name,attr"`
	Time        string            `xml:"time,attr"`
	SkipMessage *JUnitSkipMessage `xml:"skipped,omitempty"`
	Failure     *JUnitFailure     `xml:"failure,omitempty"`
	Properties  JUnitProperties   `xml:"properties,omitempty"`
}

// JUnitSkipMessage contains the reason why a testcase was skipped.
type JUnitSkipMessage struct {
	Message string `xml:"message,attr"`
}

// JUnitProperties is a container for JUnitProperty
type JUnitProperties struct {
	Property []JUnitProperty
}

// JUnitProperty represents a key/value pair used to define properties.
type JUnitProperty struct {
	XMLName xml.Name `xml:"property,omitempty"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:"value,attr"`
}

// JUnitFailure contains data related to a failed test.
type JUnitFailure struct {
	Message  string `xml:"message,attr"`
	Type     string `xml:"type,attr"`
	Contents string `xml:",chardata"`
}

// Config used to write a junit XML document.
type Config struct {
	ProjectName             string
	FormatTestSuiteName     FormatFunc
	FormatTestCaseClassname FormatFunc
	HideEmptyPackages       bool
	// This is used for tests to have a consistent timestamp
	customTimestamp string
	customElapsed   string
}

// FormatFunc converts a string from one format into another.
type FormatFunc func(string) string

// Write creates an XML document and writes it to out.
func Write(out io.Writer, exec *testjson.Execution, cfg Config) error {
	if err := write(out, generate(exec, cfg)); err != nil {
		return fmt.Errorf("failed to write JUnit XML: %v", err)
	}
	return nil
}

func generate(exec *testjson.Execution, cfg Config) JUnitTestSuites {
	cfg = configWithDefaults(cfg)
	version := goVersion()
	suites := JUnitTestSuites{
		Name:     cfg.ProjectName,
		Tests:    exec.Total(),
		Failures: len(exec.Failed()),
		Errors:   len(exec.Errors()),
		Time:     formatDurationAsSeconds(time.Since(exec.Started())),
	}

	if cfg.customElapsed != "" {
		suites.Time = cfg.customElapsed
	}
	for _, pkgname := range exec.Packages() {
		pkg := exec.Package(pkgname)
		if cfg.HideEmptyPackages && pkg.IsEmpty() {
			continue
		}
		properties := JUnitProperties{packageProperties(version)}
		junitpkg := JUnitTestSuite{
			Name:       cfg.FormatTestSuiteName(pkgname),
			Tests:      pkg.Total,
			Time:       formatDurationAsSeconds(pkg.Elapsed()),
			Properties: properties,
			TestCases:  packageTestCases(pkg, cfg.FormatTestCaseClassname),
			Failures:   len(pkg.Failed),
			Timestamp:  cfg.customTimestamp,
		}
		if cfg.customTimestamp == "" {
			junitpkg.Timestamp = exec.Started().Format(time.RFC3339)
		}
		suites.Suites = append(suites.Suites, junitpkg)
	}
	return suites
}

func configWithDefaults(cfg Config) Config {
	noop := func(v string) string {
		return v
	}
	if cfg.FormatTestSuiteName == nil {
		cfg.FormatTestSuiteName = noop
	}
	if cfg.FormatTestCaseClassname == nil {
		cfg.FormatTestCaseClassname = noop
	}
	return cfg
}

func formatDurationAsSeconds(d time.Duration) string {
	return fmt.Sprintf("%f", d.Seconds())
}

func packageProperties(goVersion string) []JUnitProperty {
	return []JUnitProperty{
		{Name: "go.version", Value: goVersion},
	}
}

// goVersion returns the version as reported by the go binary in PATH. This
// version will not be the same as runtime.Version, which is always the version
// of go used to build the gotestsum binary.
//
// To skip the os/exec call set the GOVERSION environment variable to the
// desired value.
func goVersion() string {
	if version, ok := os.LookupEnv("GOVERSION"); ok {
		return version
	}
	log.Debugf("exec: go version")
	cmd := exec.Command("go", "version")
	out, err := cmd.Output()
	if err != nil {
		log.Warnf("Failed to lookup go version for junit xml: %v", err)
		return "unknown"
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "go version ")
}

func packageTestCases(pkg *testjson.Package, formatClassname FormatFunc) []JUnitTestCase {
	cases := []JUnitTestCase{}

	if pkg.TestMainFailed() {
		jtc := newJUnitTestCase(testjson.TestCase{Test: "TestMain"}, formatClassname)
		jtc.Failure = &JUnitFailure{
			Message:  "Failed",
			Contents: pkg.Output(0),
		}
		cases = append(cases, jtc)
	}

	for _, tc := range pkg.Failed {
		jtc := newJUnitTestCase(tc, formatClassname)
		jtc.Failure = &JUnitFailure{
			Message:  "Failed",
			Contents: strings.Join(pkg.OutputLines(tc), ""),
		}
		cases = append(cases, jtc)
	}

	for _, tc := range pkg.Skipped {
		jtc := newJUnitTestCase(tc, formatClassname)
		jtc.SkipMessage = &JUnitSkipMessage{
			Message: strings.Join(pkg.OutputLines(tc), ""),
		}
		cases = append(cases, jtc)
	}

	for _, tc := range pkg.Passed {
		jtc := newJUnitTestCase(tc, formatClassname)
		cases = append(cases, jtc)
	}
	return cases
}

func newJUnitTestCase(tc testjson.TestCase, formatClassname FormatFunc) JUnitTestCase {
	props, strippedName := extractRequirementFromName(tc.Test.Name())
	return JUnitTestCase{
		Classname:  formatClassname(tc.Package),
		Name:       strippedName,
		Time:       formatDurationAsSeconds(tc.Elapsed),
		Properties: JUnitProperties{props},
	}
}

func extractRequirementFromName(name string) (props []JUnitProperty, strippedName string) {

	// Find the opening and closing square brackets in the name
	openingBracketIndex := strings.Index(name, "[")
	closingBracketIndex := strings.Index(name, "]")

	if openingBracketIndex != -1 && closingBracketIndex != -1 {
		// Extract
		substring := name[openingBracketIndex+1 : closingBracketIndex]
		strippedName = strings.ReplaceAll(name, "["+substring+"]", "")
		// Split the substring using commas as delimiters and create requirement properties
		values := strings.Split(substring, ",")
		if len(values) == 1 {
			property := JUnitProperty{Name: "Requirement", Value: values[0]}
			props = append(props, property)
			return props, strippedName
		} else {
			var value string
			for i, v := range values {
				if i == 0 {
					value = v
				} else {
					value = value + "," + v
				}
			}
			property := JUnitProperty{Name: "Requirements", Value: value}
			props = append(props, property)
			return props, strippedName
		}
	}
	return nil, name
}

// Marshals the JUnitProperties into XML. Returns nil if no properties are set,
// allowing the omitempty xml tag to function correctly.
func (prop JUnitProperties) MarshalXML(e *xml.Encoder, start xml.StartElement) (err error) {
	if len(prop.Property) == 0 {
		return nil
	}

	err = e.EncodeToken(start)
	if err != nil {
		return
	}
	err = e.Encode(prop.Property)
	if err != nil {
		return
	}
	return e.EncodeToken(xml.EndElement{
		Name: start.Name,
	})
}

func write(out io.Writer, suites JUnitTestSuites) error {
	doc, err := xml.MarshalIndent(suites, "", "\t")
	if err != nil {
		return err
	}
	_, err = out.Write([]byte(xml.Header))
	if err != nil {
		return err
	}
	_, err = out.Write(doc)
	return err
}
