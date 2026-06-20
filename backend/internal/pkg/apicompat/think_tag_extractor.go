package apicompat

import "strings"

// ThinkTagExtractor separates inline `<think>...</think>` reasoning blocks from
// the user-visible text of a Chat Completions stream.
//
// Some domestic models (and other OpenAI-compatible upstreams) do not expose a
// structured `reasoning_content` field; instead they embed their chain-of-thought
// directly in the assistant `content` wrapped in `<think>...</think>` tags, e.g.
//
//	<think>
//	the model is thinking...
//	</think>
//	the actual answer
//
// If forwarded verbatim, the `<think>` block leaks into the user-visible output
// (Responses `output_text`). This extractor strips those blocks, returning the
// visible text and the reasoning text separately.
//
// It is a streaming state machine: tags may be split across arbitrary Push
// boundaries (e.g. `<thi` then `nk>`), so any partial-tag suffix is buffered
// internally and resolved once more bytes arrive. Call Flush at end-of-stream to
// drain whatever remains.
//
// Design rules (see task spec):
//   - Recognises `<think>` / `</think>` only, case-insensitively (`<THINK>` etc.).
//   - Tags may span chunks.
//   - Supports multiple think blocks in one stream.
//   - Text before/between/after think blocks (including leading whitespace) is
//     emitted as visible verbatim — never trimmed or mutated.
//   - An unclosed `<think>` keeps its body in reasoning, never visible.
//   - Only an exact `<think>` is treated as an opening tag: lookalikes such as
//     `<thinking>`, `<thinker>` or a bare `<` are passed through as visible text.
//
// ThinkTagExtractor is not safe for concurrent use; use one per stream.
type ThinkTagExtractor struct {
	inThink bool
	// pending holds a trailing `<...` fragment that could be the start of a
	// (possibly cross-chunk) tag but is not yet long enough to classify.
	pending string
}

const (
	thinkOpenTag  = "<think>"
	thinkCloseTag = "</think>"
)

// tagMatch classifies how a string beginning with '<' relates to a target tag.
type tagMatch int

const (
	tagNoMatch tagMatch = iota // diverges from the tag — '<' is literal text
	tagPartial                 // s is a proper prefix of the tag — need more bytes
	tagFull                    // s begins with the complete tag
)

// matchTag compares s (which must start at a '<') against tagLower (an all
// lowercase-ASCII tag) case-insensitively. tagLower is ASCII, so any non-ASCII
// byte in s simply fails to match and yields tagNoMatch.
func matchTag(s, tagLower string) tagMatch {
	n := len(s)
	if n > len(tagLower) {
		n = len(tagLower)
	}
	for i := 0; i < n; i++ {
		if asciiToLower(s[i]) != tagLower[i] {
			return tagNoMatch
		}
	}
	if len(s) >= len(tagLower) {
		return tagFull
	}
	return tagPartial
}

func asciiToLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// Push consumes the next chunk of assistant content and returns the visible and
// reasoning text decoded so far. Either return value may be empty. Bytes that
// might belong to a not-yet-complete tag are buffered and surfaced on a later
// Push or on Flush.
func (e *ThinkTagExtractor) Push(text string) (visible string, reasoning string) {
	if text == "" && e.pending == "" {
		return "", ""
	}

	var vis strings.Builder
	var reason strings.Builder

	work := e.pending + text
	e.pending = ""

	i := 0
	for i < len(work) {
		if !e.inThink {
			lt := strings.IndexByte(work[i:], '<')
			if lt < 0 {
				vis.WriteString(work[i:])
				break
			}
			vis.WriteString(work[i : i+lt])
			i += lt
			switch matchTag(work[i:], thinkOpenTag) {
			case tagFull:
				e.inThink = true
				i += len(thinkOpenTag)
			case tagPartial:
				e.pending = work[i:]
				return vis.String(), reason.String()
			default: // tagNoMatch
				vis.WriteByte('<')
				i++
			}
		} else {
			lt := strings.IndexByte(work[i:], '<')
			if lt < 0 {
				reason.WriteString(work[i:])
				break
			}
			reason.WriteString(work[i : i+lt])
			i += lt
			switch matchTag(work[i:], thinkCloseTag) {
			case tagFull:
				e.inThink = false
				i += len(thinkCloseTag)
			case tagPartial:
				e.pending = work[i:]
				return vis.String(), reason.String()
			default: // tagNoMatch
				reason.WriteByte('<')
				i++
			}
		}
	}

	return vis.String(), reason.String()
}

// Flush drains any buffered partial-tag bytes at end-of-stream.
//
//   - Inside an (unclosed) think block, the buffered bytes are reasoning and must
//     never be revealed as visible.
//   - Outside a think block, a buffered `<...` prefix that never completed into a
//     real tag was just literal text and is emitted as visible.
func (e *ThinkTagExtractor) Flush() (visible string, reasoning string) {
	pending := e.pending
	e.pending = ""
	if pending == "" {
		return "", ""
	}
	if e.inThink {
		return "", pending
	}
	return pending, ""
}
