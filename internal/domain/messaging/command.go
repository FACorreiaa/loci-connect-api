package messaging

import "strings"

type command int

const (
	cmdNone command = iota
	cmdStart
	cmdHelp
	cmdUnlink
	cmdMore
	cmdUnknown
)

// parseCommand recognises a leading slash command and its argument.
//
// Handles the "@botname" suffix Telegram appends in group chats, where "/help"
// arrives as "/help@loci_bot" and a plain prefix match would miss it.
func parseCommand(text string) (command, string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return cmdNone, ""
	}

	head, rest, _ := strings.Cut(text[1:], " ")
	if at := strings.IndexByte(head, '@'); at >= 0 {
		head = head[:at]
	}

	arg := strings.TrimSpace(rest)
	switch strings.ToLower(head) {
	case "start":
		return cmdStart, arg
	case "help":
		return cmdHelp, arg
	case "unlink", "disconnect":
		return cmdUnlink, arg
	case "more":
		return cmdMore, arg
	default:
		return cmdUnknown, arg
	}
}
