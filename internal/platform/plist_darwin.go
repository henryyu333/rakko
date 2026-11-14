//go:build darwin

package platform

// Top-level property-list values of a file, for Safari's `.download`
// bundles: their Info.plist is normally binary, which the standard library
// cannot read. NSDictionary reads both XML and binary property lists; a
// string that is not a property list at all yields ok == false.

// PlistValue is one top-level value: a string or a whole number. Dates,
// data and nested containers are skipped.
type PlistValue struct {
	Text     string
	Number   int64
	IsNumber bool
}

// PlistValues reads path as a property-list dictionary.
func PlistValues(path string) (map[string]PlistValue, bool) {
	load()
	var out map[string]PlistValue
	ok := false
	withPool(func() {
		d := send(class("NSDictionary"), "dictionaryWithContentsOfFile:", uintptr(nsString(path)))
		if d == 0 {
			return
		}
		keys := send(d, "allKeys")
		n := sendInt(keys, "count")
		out = make(map[string]PlistValue, n)
		numberClass, stringClass := class("NSNumber"), class("NSString")
		for i := range n {
			k := send(keys, "objectAtIndex:", uintptr(i))
			v := send(d, "objectForKey:", uintptr(k))
			if v == 0 {
				continue
			}
			name := goString(k)
			switch {
			case sendInt(v, "isKindOfClass:", uintptr(numberClass)) != 0:
				out[name] = PlistValue{Number: sendInt64(v, "longLongValue"), IsNumber: true}
			case sendInt(v, "isKindOfClass:", uintptr(stringClass)) != 0:
				out[name] = PlistValue{Text: goString(v)}
			}
		}
		ok = true
	})
	return out, ok
}
