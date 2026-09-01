/*
 * vscodeuri.go
 * Copyright (c) Microsoft Corporation. All rights reserved.
 * Licensed under the MIT license.
 *
 * The parts of the `vscode-uri` package (v3.1.0) that pyright's Uri classes
 * depend on: URI.parse, URI.file, URI#with, URI#fsPath, URI#toString and
 * Utils.resolvePath.
 *
 * Transliterated from vscode-uri/lib/umd/uri.js and utils.js -- the published
 * JavaScript rather than its TypeScript source, because that is what pyright
 * actually runs against.
 *
 * This is a dependency of the port, not part of pyright, but it cannot be
 * skipped: FileUri and WebUri hand their string forms to it, and the exact
 * percent-encoding it produces is what ends up in a Uri's key. Two URIs that
 * encode differently are two different cache entries all the way up through the
 * import resolver.
 *
 * `isWindows` below is `process.platform === 'win32'` in the original, read at
 * module load. It is runtime.GOOS here for the same reason common.PathSep is
 * os.PathSeparator: so both sides agree on whatever host they run on.
 */

package uri

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"
)

var isWindows = runtime.GOOS == "windows"

const (
	uriEmpty = ""
	uriSlash = "/"
)

var (
	schemePattern    = regexp.MustCompile(`^\w[\w\d+.-]*$`)
	singleSlashStart = regexp.MustCompile(`^/`)
	doubleSlashStart = regexp.MustCompile(`^//`)

	// uriRegexp always matches: every group is optional. The lazy `+?` in the
	// scheme group and Go's leftmost-first (Perl, not POSIX) matching make this
	// behave the same as the JavaScript RegExp.
	uriRegexp = regexp.MustCompile(`^(([^:/?#]+?):)?(//([^/?#]*))?([^?#]*)(\?([^#]*))?(#(.*))?`)
)

// vsURI is vscode-uri's URI class. The `Uri` subclass in the original exists
// only to memoize _formatted and _fsPath, so those fields live here directly.
type vsURI struct {
	scheme    string
	authority string
	path      string
	query     string
	fragment  string

	formatted string
	fsPath    string
}

// uriError is what _validateUri throws. It reaches the bridge as a thrown
// JavaScript Error, which is what the TypeScript tests assert on.
type uriError struct{ message string }

func (e *uriError) Error() string { return e.message }

func throwUriError(format string, args ...any) {
	panic(&uriError{message: fmt.Sprintf(format, args...)})
}

func validateUri(ret *vsURI, strict bool) {
	// scheme, must be set
	if ret.scheme == "" && strict {
		throwUriError(`[UriError]: Scheme is missing: {scheme: "", authority: "%s", path: "%s", query: "%s", fragment: "%s"}`,
			ret.authority, ret.path, ret.query, ret.fragment)
	}

	// scheme, https://tools.ietf.org/html/rfc3986#section-3.1
	// ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )
	if ret.scheme != "" && !schemePattern.MatchString(ret.scheme) {
		throwUriError("[UriError]: Scheme contains illegal characters.")
	}

	// path, http://tools.ietf.org/html/rfc3986#section-3.3
	// If a URI contains an authority component, then the path component must
	// either be empty or begin with a slash ("/") character. If a URI does not
	// contain an authority component, then the path cannot begin with two slash
	// characters ("//").
	if ret.path != "" {
		if ret.authority != "" {
			if !singleSlashStart.MatchString(ret.path) {
				throwUriError(`[UriError]: If a URI contains an authority component, then the path component must either be empty or begin with a slash ("/") character`)
			}
		} else {
			if doubleSlashStart.MatchString(ret.path) {
				throwUriError(`[UriError]: If a URI does not contain an authority component, then the path cannot begin with two slash characters ("//")`)
			}
		}
	}
}

// schemeFix carries the original's comment: for a while URIs without schemes
// were allowed, and this is the migration for them. An unschemed URI in
// non-strict mode falls back to the file scheme.
func schemeFix(scheme string, strict bool) string {
	if scheme == "" && !strict {
		return "file"
	}
	return scheme
}

// referenceResolution implements a bit of
// https://tools.ietf.org/html/rfc3986#section-5.
func referenceResolution(scheme string, path string) string {
	// The slash-character is the 'default base', as constructing URIs relative
	// to other URIs is not supported. This also means paths are altered and
	// potentially broken. See https://tools.ietf.org/html/rfc3986#section-5.1.4
	switch scheme {
	case "https", "http", "file":
		if path == "" {
			path = uriSlash
		} else if path[0] != '/' {
			path = uriSlash + path
		}
	}
	return path
}

// newVsURI is the string form of the constructor: scheme fix, reference
// resolution and validation all apply.
func newVsURI(scheme, authority, path, query, fragment string, strict bool) *vsURI {
	u := &vsURI{
		scheme:    schemeFix(scheme, strict),
		authority: authority,
		query:     query,
		fragment:  fragment,
	}
	u.path = referenceResolution(u.scheme, path)
	validateUri(u, strict)
	return u
}

// newVsURIFromComponents is the object form of the constructor, which the
// original reaches through URI.revive and URI#with. It does no validation,
// because these components came from a URI in the first place.
func newVsURIFromComponents(scheme, authority, path, query, fragment string) *vsURI {
	return &vsURI{
		scheme:    scheme,
		authority: authority,
		path:      path,
		query:     query,
		fragment:  fragment,
	}
}

// uriChange is the argument of URI#with. A nil field means "undefined" -- keep
// the current value. The original also accepts null to mean "clear", which no
// pyright caller uses.
type uriChange struct {
	scheme    *string
	authority *string
	path      *string
	query     *string
	fragment  *string
}

func (u *vsURI) with(change uriChange) *vsURI {
	scheme := u.scheme
	if change.scheme != nil {
		scheme = *change.scheme
	}
	authority := u.authority
	if change.authority != nil {
		authority = *change.authority
	}
	path := u.path
	if change.path != nil {
		path = *change.path
	}
	query := u.query
	if change.query != nil {
		query = *change.query
	}
	fragment := u.fragment
	if change.fragment != nil {
		fragment = *change.fragment
	}

	if scheme == u.scheme && authority == u.authority && path == u.path &&
		query == u.query && fragment == u.fragment {
		return u
	}

	// The subclass constructor, which reapplies schemeFix and
	// referenceResolution but not validation (_strict defaults to false).
	return newVsURI(scheme, authority, path, query, fragment, false)
}

// vsURIParse creates a new URI from a string, e.g. `http://www.example.com/a`,
// `file:///usr/home`, or `scheme:with/path`.
func vsURIParse(value string, strict bool) *vsURI {
	match := uriRegexp.FindStringSubmatch(value)
	if match == nil {
		return newVsURIFromComponents(uriEmpty, uriEmpty, uriEmpty, uriEmpty, uriEmpty)
	}
	return newVsURI(
		match[2],
		percentDecode(match[4]),
		percentDecode(match[5]),
		percentDecode(match[7]),
		percentDecode(match[9]),
		strict,
	)
}

// vsURIFile creates a new URI from a file system path, e.g. `c:\my\files`,
// `/usr/home`, or `\\server\share\some\path`.
//
// The difference from vsURIParse is that the argument is treated as a path, not
// as a stringified URI, so '#' and '?' in it are not interpreted.
func vsURIFile(path string) *vsURI {
	authority := uriEmpty

	// Normalize to forward slashes on Windows; on other systems backslashes are
	// valid filename characters, e.g. /f\oo/ba\r.txt
	if isWindows {
		path = strings.ReplaceAll(path, "\\", uriSlash)
	}

	// Check for an authority as used in UNC shares, or use the path as given.
	if len(path) > 1 && path[0] == '/' && path[1] == '/' {
		idx := strings.Index(path[2:], uriSlash)
		if idx == -1 {
			authority = path[2:]
			path = uriSlash
		} else {
			idx += 2
			authority = path[2:idx]
			path = path[idx:]
			if path == "" {
				path = uriSlash
			}
		}
	}

	return newVsURI("file", authority, path, uriEmpty, uriEmpty, false)
}

// getFsPath computes `fsPath` for the URI.
func (u *vsURI) getFsPath() string {
	if u.fsPath == "" {
		u.fsPath = uriToFsPath(u, false)
	}
	return u.fsPath
}

func (u *vsURI) String() string {
	if u.formatted == "" {
		u.formatted = asFormatted(u, false)
	}
	return u.formatted
}

// reserved characters: https://tools.ietf.org/html/rfc3986#section-2.2
var encodeTable = map[byte]string{
	':':  "%3A",
	'/':  "%2F",
	'?':  "%3F",
	'#':  "%23",
	'[':  "%5B",
	']':  "%5D",
	'@':  "%40",
	'!':  "%21",
	'$':  "%24",
	'&':  "%26",
	'\'': "%27",
	'(':  "%28",
	')':  "%29",
	'*':  "%2A",
	'+':  "%2B",
	',':  "%2C",
	';':  "%3B",
	'=':  "%3D",
	' ':  "%20",
}

// encodeURIComponentFast is the original's hand-rolled encoder, which only
// falls back to JavaScript's encodeURIComponent for characters its table does
// not cover.
//
// The original walks UTF-16 code units and delegates any run it does not
// recognize to encodeURIComponent, which re-encodes that run as UTF-8. Walking
// bytes here reaches the same answer: every code unit the fast path accepts is
// ASCII, so a multi-byte UTF-8 sequence always lands wholly in the delegated
// run, and percent-encoding it byte by byte is exactly what
// encodeURIComponent does.
func encodeURIComponentFast(uriComponent string, isPath bool, isAuthority bool) string {
	var sb strings.Builder
	allocated := false

	for pos := 0; pos < len(uriComponent); pos++ {
		code := uriComponent[pos]

		// unreserved characters: https://tools.ietf.org/html/rfc3986#section-2.3
		if (code >= 'a' && code <= 'z') ||
			(code >= 'A' && code <= 'Z') ||
			(code >= '0' && code <= '9') ||
			code == '-' ||
			code == '.' ||
			code == '_' ||
			code == '~' ||
			(isPath && code == '/') ||
			(isAuthority && code == '[') ||
			(isAuthority && code == ']') ||
			(isAuthority && code == ':') {
			if allocated {
				sb.WriteByte(code)
			}
			continue
		}

		// Encoding needed. The original allocates the result string here,
		// seeding it with everything accepted so far.
		if !allocated {
			sb.WriteString(uriComponent[:pos])
			allocated = true
		}

		if escaped, ok := encodeTable[code]; ok {
			sb.WriteString(escaped)
			continue
		}

		// The encodeURIComponent fallback. Nothing that reaches here is in
		// encodeURIComponent's own unreserved set -- those are all either
		// accepted above or in the table -- so every byte is escaped.
		sb.WriteString(fmt.Sprintf("%%%02X", code))
	}

	if !allocated {
		return uriComponent
	}
	return sb.String()
}

// encodeURIComponentMinimal is the skipEncoding encoder: only '#' and '?'.
func encodeURIComponentMinimal(path string) string {
	var sb strings.Builder
	allocated := false

	for pos := 0; pos < len(path); pos++ {
		code := path[pos]
		if code == '#' || code == '?' {
			if !allocated {
				sb.WriteString(path[:pos])
				allocated = true
			}
			sb.WriteString(encodeTable[code])
		} else if allocated {
			sb.WriteByte(code)
		}
	}

	if !allocated {
		return path
	}
	return sb.String()
}

// uriToFsPath computes `fsPath` for the given uri.
func uriToFsPath(u *vsURI, keepDriveLetterCasing bool) string {
	var value string

	charAt := func(i int) byte {
		if i < len(u.path) {
			return u.path[i]
		}
		return 0
	}

	switch {
	case u.authority != "" && len(u.path) > 1 && u.scheme == "file":
		// UNC path: file://shares/c$/far/boo
		value = "//" + u.authority + u.path

	case charAt(0) == '/' &&
		((charAt(1) >= 'A' && charAt(1) <= 'Z') || (charAt(1) >= 'a' && charAt(1) <= 'z')) &&
		charAt(2) == ':':
		if !keepDriveLetterCasing {
			// Windows drive letter: file:///c:/far/boo
			value = strings.ToLower(u.path[1:2]) + u.path[2:]
		} else {
			value = u.path[1:]
		}

	default:
		value = u.path
	}

	if isWindows {
		value = strings.ReplaceAll(value, "/", "\\")
	}
	return value
}

// asFormatted creates the external version of a uri.
func asFormatted(u *vsURI, skipEncoding bool) string {
	encoder := encodeURIComponentFast
	if skipEncoding {
		encoder = func(s string, isPath bool, isAuthority bool) string {
			return encodeURIComponentMinimal(s)
		}
	}

	res := ""
	scheme, authority, path, query, fragment := u.scheme, u.authority, u.path, u.query, u.fragment

	if scheme != "" {
		res += scheme
		res += ":"
	}
	if authority != "" || scheme == "file" {
		res += uriSlash
		res += uriSlash
	}
	if authority != "" {
		if idx := strings.Index(authority, "@"); idx != -1 {
			// <user>@<auth>
			userinfo := authority[:idx]
			authority = authority[idx+1:]
			idx = strings.LastIndex(userinfo, ":")
			if idx == -1 {
				res += encoder(userinfo, false, false)
			} else {
				// <user>:<pass>@<auth>
				res += encoder(userinfo[:idx], false, false)
				res += ":"
				res += encoder(userinfo[idx+1:], false, true)
			}
			res += "@"
		}

		authority = strings.ToLower(authority)
		if idx := strings.LastIndex(authority, ":"); idx == -1 {
			res += encoder(authority, false, true)
		} else {
			// <auth>:<port>
			res += encoder(authority[:idx], false, true)
			res += authority[idx:]
		}
	}
	if path != "" {
		// Lower-case Windows drive letters in /C:/fff or C:/fff.
		if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			if code := path[1]; code >= 'A' && code <= 'Z' {
				path = "/" + string(code+32) + ":" + path[3:] // "/c:".length === 3
			}
		} else if len(path) >= 2 && path[1] == ':' {
			if code := path[0]; code >= 'A' && code <= 'Z' {
				path = string(code+32) + ":" + path[2:]
			}
		}

		// Encode the rest of the path.
		res += encoder(path, true, false)
	}
	if query != "" {
		res += "?"
		res += encoder(query, false, false)
	}
	if fragment != "" {
		res += "#"
		if !skipEncoding {
			res += encodeURIComponentFast(fragment, false, false)
		} else {
			res += fragment
		}
	}
	return res
}

// --- decode

var encodedAsHex = regexp.MustCompile(`(%[0-9A-Za-z][0-9A-Za-z])+`)

// percentDecode replaces each run of %XX escapes with its decoded form.
//
// Note the character class is [0-9A-Za-z], not [0-9A-Fa-f], so "%zz" matches
// and then fails to decode -- which is what decodeURIComponentGraceful is for.
func percentDecode(str string) string {
	if !encodedAsHex.MatchString(str) {
		return str
	}
	return encodedAsHex.ReplaceAllStringFunc(str, decodeURIComponentGraceful)
}

// decodeURIComponentGraceful is decodeURIComponent with the original's fallback:
// on failure, keep the first escape verbatim and try again on the rest.
func decodeURIComponentGraceful(str string) string {
	if decoded, ok := decodeURIComponent(str); ok {
		return decoded
	}
	if len(str) > 3 {
		return str[:3] + decodeURIComponentGraceful(str[3:])
	}
	return str
}

// decodeURIComponent is JavaScript's decodeURIComponent restricted to the input
// percentDecode hands it: a run of %XX triples. It reports failure where the
// original throws URIError -- for a non-hex escape, and for byte sequences that
// are not well-formed UTF-8.
//
// JavaScript decodes to UTF-16 and rejects surrogate and out-of-range values;
// Go's utf8.Valid rejects exactly the same encodings, along with the overlong
// forms both reject.
func decodeURIComponent(str string) (string, bool) {
	if len(str)%3 != 0 {
		return "", false
	}
	bytes := make([]byte, 0, len(str)/3)
	for i := 0; i < len(str); i += 3 {
		hi, ok1 := hexValue(str[i+1])
		lo, ok2 := hexValue(str[i+2])
		if str[i] != '%' || !ok1 || !ok2 {
			return "", false
		}
		bytes = append(bytes, byte(hi<<4|lo))
	}
	if !utf8.Valid(bytes) {
		return "", false
	}
	return string(bytes), true
}

func hexValue(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

/*
 * vscode-uri/lib/umd/utils.js -- only resolvePath, which is what
 * pyright's normalizeUri calls.
 */

// utilsResolvePath resolves one or more paths against the path of a URI, using
// '/' as the directory separator. All '..' and '.' segments are resolved,
// repeated separators are collapsed, and trailing separators are removed.
func utilsResolvePath(u *vsURI, paths ...string) *vsURI {
	path := u.path
	slashAdded := false
	if len(path) == 0 || path[0] != '/' {
		// Make the path absolute: posixPath.resolve uses the cwd otherwise.
		path = uriSlash + path
		slashAdded = true
	}

	resolvedPath := posixResolve(append([]string{path}, paths...))

	if slashAdded && len(resolvedPath) > 0 && resolvedPath[0] == '/' && u.authority == "" {
		resolvedPath = resolvedPath[1:]
	}
	return u.with(uriChange{path: &resolvedPath})
}

// posixResolve is path.posix.resolve.
//
// The original falls back to process.cwd() when no argument is absolute.
// utilsResolvePath is the only caller and it always passes an absolute first
// path, so that branch is unreachable; it panics rather than inventing a
// working directory, so a future caller finds out immediately.
func posixResolve(args []string) string {
	resolvedPath := ""
	resolvedAbsolute := false

	for i := len(args) - 1; i >= 0 && !resolvedAbsolute; i-- {
		path := args[i]
		if len(path) == 0 {
			continue
		}
		resolvedPath = path + "/" + resolvedPath
		resolvedAbsolute = path[0] == '/'
	}

	if !resolvedAbsolute {
		panic("posixResolve: no absolute path and no working directory")
	}

	return "/" + normalizeStringPosix(resolvedPath, false)
}

// normalizeStringPosix is Node's internal normalizeString for POSIX paths. It
// is the same routine common/pathutils.go transliterates; it is repeated here
// because that one lives in a package this one must not import.
func normalizeStringPosix(path string, allowAboveRoot bool) string {
	res := ""
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var code byte

	for i := 0; i <= len(path); i++ {
		if i < len(path) {
			code = path[i]
		} else if code == '/' {
			break
		} else {
			code = '/'
		}

		if code == '/' {
			if lastSlash == i-1 || dots == 1 {
				// NOOP
			} else if dots == 2 {
				if len(res) < 2 || lastSegmentLength != 2 ||
					res[len(res)-1] != '.' || res[len(res)-2] != '.' {
					if len(res) > 2 {
						lastSlashIndex := strings.LastIndexByte(res, '/')
						if lastSlashIndex == -1 {
							res = ""
							lastSegmentLength = 0
						} else {
							res = res[:lastSlashIndex]
							lastSegmentLength = len(res) - 1 - strings.LastIndexByte(res, '/')
						}
						lastSlash = i
						dots = 0
						continue
					} else if len(res) != 0 {
						res = ""
						lastSegmentLength = 0
						lastSlash = i
						dots = 0
						continue
					}
				}
				if allowAboveRoot {
					if len(res) > 0 {
						res += "/.."
					} else {
						res = ".."
					}
					lastSegmentLength = 2
				}
			} else {
				if len(res) > 0 {
					res += "/" + path[lastSlash+1:i]
				} else {
					res = path[lastSlash+1 : i]
				}
				lastSegmentLength = i - lastSlash - 1
			}
			lastSlash = i
			dots = 0
		} else if code == '.' && dots != -1 {
			dots++
		} else {
			dots = -1
		}
	}

	return res
}
