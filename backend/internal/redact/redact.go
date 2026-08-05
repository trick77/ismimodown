// Package redact strips credentials from text on its way to a log.
//
// It exists because two packages now need the same rule and a security regex
// that lives in two files drifts: httpapi redacts the cause of a 5xx, and the
// scheduler redacts the provider's error body before quoting it on an inference
// call's log line. AGENTS.md states the constraint both are serving — the MiMo
// key is live and billable and the repo is public, so it may never reach a log
// line, and a partial key is still key material.
package redact

import (
	"errors"
	"regexp"
)

// queryStringRe matches a URL query string, from "?" up to (but not including)
// the next whitespace or double-quote character — that's how url.Error and most
// transport errors delimit an embedded URL in their rendered message.
var queryStringRe = regexp.MustCompile(`\?[^\s"]*`)

// userinfoRe matches "user:pass@" userinfo embedded in a URL authority.
var userinfoRe = regexp.MustCompile(`://[^/\s"]+@`)

// bearerRe matches an Authorization bearer value. MiMo transport errors and
// provider error bodies can echo request fragments, and the token-plan key is a
// live billable credential in a public-facing service — so it is stripped from
// anything heading for the logs, not merely from anything heading for a client.
var bearerRe = regexp.MustCompile(`(?i)bearer\s+\S+`)

// String strips query strings, userinfo and bearer tokens from s.
//
// The whole query string is stripped rather than an allowlist of "safe"
// parameter names, since an allowlist is a leak waiting for the next parameter
// to be added. The URL path is left visible, since that's the useful diagnostic.
//
// Callers that also bound the length must redact FIRST: clipping a bearer token
// in half leaves the first half of a live key in the log.
func String(s string) string {
	s = queryStringRe.ReplaceAllString(s, "")
	s = userinfoRe.ReplaceAllString(s, "://")
	return bearerRe.ReplaceAllString(s, "Bearer [redacted]")
}

// Err strips the same things from an error's rendered message.
//
// Redaction operates on the rendered string rather than mutating an inner
// *url.Error, because fmt.Errorf's %w renders and freezes the wrapped error's
// message at wrap time — mutating a struct field inside it afterwards has no
// effect on what the outer error's Error() returns. Working on the final string
// is immune to wrapping depth and to how the underlying libraries nest errors.
//
// When redaction changes the message, the returned error is a plain errors.New
// of the redacted text, which does NOT preserve errors.Is/As against the
// original sentinel or type. That is acceptable only because the result is used
// solely as a log attribute, never for control flow — do not use its result in
// an errors.Is/As check. When nothing was redacted, the original err (and its
// full chain) is returned unchanged.
func Err(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	redacted := String(msg)
	if redacted == msg {
		return err
	}
	return errors.New(redacted)
}
