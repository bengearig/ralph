package docexport

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

// MarkerInfo is what a generated file's marker line records.
type MarkerInfo struct {
	ID      string
	Version int
}

var markerPattern = regexp.MustCompile(`^<!-- ralph:generated document=(D-[0-9]+) version=([0-9]+) -->$`)

// Marker returns the machine marker that is the whole first line of an exported file.
func Marker(id string, version int) string {
	return fmt.Sprintf("<!-- ralph:generated document=%s version=%d -->", id, version)
}

// ParseMarker reads the first line of content and reports whether it is exactly a marker.
func ParseMarker(content []byte) (MarkerInfo, bool) {
	line := content
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = bytes.TrimSuffix(line, []byte("\r"))
	m := markerPattern.FindSubmatch(line)
	if m == nil {
		return MarkerInfo{}, false
	}
	version, err := strconv.Atoi(string(m[2]))
	if err != nil {
		return MarkerInfo{}, false
	}
	return MarkerInfo{ID: string(m[1]), Version: version}, true
}
