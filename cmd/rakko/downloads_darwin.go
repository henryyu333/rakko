//go:build darwin

package main

// Safari's own download bundles: the account of a `.download` folder and
// the property-list reader it needs. The rest of the downloads module is
// shared.
import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rakko/internal/platform"
)

// safariDownload reads one Safari `.download` bundle. Its Info.plist names
// the bytes so far, the total to load and the final path; when the keys are
// missing the bundle name is still a usable target.
func safariDownload(bundle string) (downloadFile, bool) {
	vals, ok := readPlist(filepath.Join(bundle, "Info.plist"))
	if !ok {
		return downloadFile{}, false
	}
	num := func(key string) int64 {
		if v, ok := vals[key]; ok && v.IsNumber {
			return v.Number
		}
		return 0
	}
	target := ""
	if v, ok := vals["DownloadEntryPath"]; ok {
		target = v.Text
	}
	if target == "" {
		base := strings.TrimSuffix(bundle, ".download")
		target = base
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(bundle), target)
	}
	var mod time.Time
	if st, err := os.Stat(filepath.Join(bundle, "Info.plist")); err == nil {
		mod = st.ModTime()
	} else if st, err := os.Stat(bundle); err == nil {
		mod = st.ModTime()
	}
	return downloadFile{
		temp: bundle, name: filepath.Base(target), target: target,
		done:  num("DownloadEntryProgressBytesSoFar"),
		total: num("DownloadEntryProgressTotalToLoad"),
		mod:   mod,
	}, true
}

// readPlist reads a property list as the platform dictionary reader does,
// with a first pass in the standard library: writing a temporary XML
// plist is how tests exercise the download path without ObjC.
var readPlist = func(path string) (map[string]platform.PlistValue, bool) {
	if vals, ok := readXMLPlist(path); ok {
		return vals, true
	}
	return platform.PlistValues(path)
}

// readXMLPlist is a minimal reader for the top-level dictionary of an XML
// property list (integers, strings and booleans). Everything else yields
// ok == false so the platform reader can try binary plists.
func readXMLPlist(path string) (map[string]platform.PlistValue, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	out := map[string]platform.PlistValue{}
	key := ""
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "key":
				var s string
				if dec.DecodeElement(&s, &t) != nil {
					return nil, false
				}
				key = s
			case "dict":
				depth++
			case "string", "integer", "real", "true", "false":
				if depth != 1 || key == "" {
					continue
				}
				switch t.Name.Local {
				case "string":
					var s string
					if dec.DecodeElement(&s, &t) != nil {
						return nil, false
					}
					out[key] = platform.PlistValue{Text: s}
				case "integer":
					var s string
					if dec.DecodeElement(&s, &t) != nil {
						return nil, false
					}
					n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
					if err != nil {
						return nil, false
					}
					out[key] = platform.PlistValue{Number: n, IsNumber: true}
				case "real":
					var s string
					if dec.DecodeElement(&s, &t) != nil {
						return nil, false
					}
					n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
					if err != nil {
						return nil, false
					}
					out[key] = platform.PlistValue{Number: int64(n), IsNumber: true}
				case "true", "false":
					v := int64(0)
					if t.Name.Local == "true" {
						v = 1
					}
					out[key] = platform.PlistValue{Number: v, IsNumber: true}
				}
				key = ""
			}
		case xml.EndElement:
			if t.Name.Local == "dict" {
				depth--
			}
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
