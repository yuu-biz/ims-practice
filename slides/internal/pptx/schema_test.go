package pptx

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The parts of the package are validated against the schemas that
// ECMA-376 publishes, which testdata/ecma376/README.md names. The
// writer makes the Transitional namespaces, so the schemas are the
// Transitional ones of Part 4; the package itself follows Part 2.
const schemaDir = "testdata/ecma376"

// noSchema marks a part that the published schemas cannot validate.
const noSchema = "none"

// schemaOf names the schema of a part, by the name the writer gives the
// part. A part that no published schema can validate gives noSchema. A
// name the writer does not make gives the empty string, so that a new
// kind of part has to be named here before it goes into a package.
func schemaOf(part string) string {
	switch {
	case part == "[Content_Types].xml":
		return "opc-contentTypes.xsd"
	case strings.HasSuffix(part, ".rels"):
		return "opc-relationships.xsd"
	case part == "docProps/app.xml":
		return "shared-documentPropertiesExtended.xsd"
	case part == "docProps/core.xml":
		// opc-coreProperties.xsd imports the Dublin Core schemas by
		// address, and ECMA does not publish them with the standard, so
		// no validator can read it from the files of the standard
		// alone. TestPackageIsComplete reads this part instead.
		return noSchema
	case part == "ppt/theme/theme1.xml", part == "ppt/tableStyles.xml":
		return "dml-main.xsd"
	case strings.HasPrefix(part, "ppt/"):
		return "pml.xsd"
	}
	return ""
}

// validator returns the xmllint that validates the parts. Where
// PPTX_XMLLINT names one, a missing one is a failure; without it the
// test is skipped, so that the tests of the repository run on a machine
// without libxml2. The CI sets it.
func validator(t *testing.T) string {
	t.Helper()
	name, required := os.LookupEnv("PPTX_XMLLINT")
	if !required {
		name = "xmllint"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		if required {
			t.Fatalf("PPTX_XMLLINT asks for %s: %v", name, err)
		}
		t.Skipf("%v; install libxml2-utils to validate the parts against the schemas of ECMA-376", err)
	}
	return path
}

// reported is how many lines of xmllint a failure shows, and how far
// into a line. One mistake of the writer is on every slide of every
// deck, and a line names every value the schemas allow there, so the
// whole report is megabytes of the same complaint. What is cut off is
// the end of a list of allowed values; the part, the line, the element
// and the value that was refused come first.
const (
	reportedLines  = 10
	reportedLength = 300
)

// validate runs xmllint over the files of one schema and returns what
// it reported, without the names of the files that validate and with
// the directory of the parts cut from the paths that remain.
func validate(t *testing.T, xmllint, schema string, files []string, dir string) string {
	t.Helper()
	args := append([]string{"--noout", "--nonet", "--schema", filepath.Join(schemaDir, schema)}, files...)
	out, err := exec.Command(xmllint, args...).CombinedOutput()
	if err == nil {
		return ""
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasSuffix(line, " validates") {
			continue
		}
		line = strings.ReplaceAll(line, dir+string(filepath.Separator), "")
		if letters := []rune(line); len(letters) > reportedLength {
			line = string(letters[:reportedLength]) + "..."
		}
		lines = append(lines, line)
	}
	if len(lines) > reportedLines {
		lines = append(lines[:reportedLines], fmt.Sprintf("and %d more lines", len(lines)-reportedLines))
	}
	return strings.Join(append(lines, err.Error()), "\n")
}

// writeParts puts the parts of one deck under dir and returns the path of
// each, by the schema that validates it.
func writeParts(t *testing.T, dir string, parts map[string][]byte) map[string][]string {
	t.Helper()
	files := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(parts)) {
		schema := schemaOf(name)
		if schema == noSchema {
			continue
		}
		if schema == "" {
			t.Errorf("%s: no schema is named for this part", name)
			continue
		}
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, parts[name], 0o644); err != nil {
			t.Fatal(err)
		}
		files[schema] = append(files[schema], file)
	}
	return files
}

// TestPartsValidateAgainstTheSchemas validates every part of the sample
// and of every deck against the schema of its kind. It is the check
// that PowerPoint itself makes when it reads a file: a part that breaks
// its schema is a file that PowerPoint offers to repair.
func TestPartsValidateAgainstTheSchemas(t *testing.T) {
	xmllint := validator(t)
	dir := t.TempDir()
	// The paths that xmllint prints are the ones below, so each deck
	// gets a directory named after its source.
	bySchema := map[string][]string{}
	for deckPath, parts := range decks(t) {
		name := strings.NewReplacer("../", "", "/", "-").Replace(deckPath)
		for schema, files := range writeParts(t, filepath.Join(dir, name), parts) {
			bySchema[schema] = append(bySchema[schema], files...)
		}
	}
	for _, schema := range slices.Sorted(maps.Keys(bySchema)) {
		files := bySchema[schema]
		slices.Sort(files)
		if problems := validate(t, xmllint, schema, files, dir); problems != "" {
			t.Errorf("%d parts against %s:\n%s", len(files), schema, problems)
		}
	}
}

// TestTheSchemaCheckFindsMistakes puts an element of a slide where its
// type does not allow it and expects the schemas to refuse the part, so
// that a check that reads no schema cannot pass as a check that does.
func TestTheSchemaCheckFindsMistakes(t *testing.T) {
	xmllint := validator(t)
	dir := t.TempDir()
	// p:extLst comes last in CT_CommonSlideData, after p:spTree.
	slide := string(build(t)["ppt/slides/slide1.xml"])
	broken := strings.Replace(slide, "<p:cSld>", "<p:cSld><p:extLst/>", 1)
	if broken == slide {
		t.Fatalf("no p:cSld in the first slide:\n%s", slide)
	}
	file := filepath.Join(dir, "slide1.xml")
	if err := os.WriteFile(file, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	problems := validate(t, xmllint, "pml.xsd", []string{file}, dir)
	if !strings.Contains(problems, "extLst") {
		t.Errorf("the schemas take a slide whose p:extLst comes first:\n%s", problems)
	}
}
