package compiler

import (
	"regexp"
	"strconv"
	"strings"
)

// A timestamp alone says nothing about an archive missing inside the window.
// Require a single, contiguous journal sequence domain spanning its boundary.
// New/unrelated sequence domains cannot establish continuity and fail closed.
func journalContinuity(older, entries []journalEntry, start, end, boot int64, allowBoot bool, first []journalEntry) (bool, string) {
	cursor := regexp.MustCompile(`(?:^|;)s=([0-9a-f]{32})(?:;|$)`)
	sequence := regexp.MustCompile(`(?:^|;)i=([0-9a-f]+)(?:;|$)`)
	timestamp := func(entry journalEntry) int64 {
		value, err := strconv.ParseInt(entry.Timestamp, 10, 64)
		if err != nil || value <= 0 {
			return -1
		}
		return value / 1000000
	}
	var previous uint64
	domain := ""
	anchored := false
	previousCursor := ""
	consume := func(entry journalEntry) string {
		stream, number := cursor.FindStringSubmatch(entry.Cursor), sequence.FindStringSubmatch(entry.Cursor)
		if len(stream) != 2 || len(number) != 2 {
			return "INVALID_CURSOR"
		}
		n, err := strconv.ParseUint(number[1], 16, 64)
		if err != nil || n == 0 {
			return "INVALID_CURSOR"
		}
		if domain != "" {
			if stream[1] != domain {
				return "SEQUENCE_DOMAIN_CHANGED"
			}
			if entry.Cursor == previousCursor {
				return ""
			}
			if previous == ^uint64(0) || n != previous+1 {
				return "SEQUENCE_GAP"
			}
		}
		domain, previous, previousCursor = stream[1], n, entry.Cursor
		return ""
	}
	if len(older) > 1 {
		return false, "INVALID_BOUNDARY"
	}
	if len(older) == 1 {
		when := timestamp(older[0])
		if when < 0 || when > start {
			return false, "INVALID_BOUNDARY"
		}
		if reason := consume(older[0]); reason != "" {
			return false, reason
		}
		anchored = true
	}
	if !anchored && allowBoot && boot >= start && len(entries) > 0 {
		when := timestamp(entries[0])
		for _, entry := range first {
			if entry.BootID != "" && entry.BootID == entries[0].BootID && when >= boot && when <= boot+60 && timestamp(entry) >= when && timestamp(entry) <= boot+60 {
				anchored = true
			}
		}
	}
	if !anchored {
		return false, "NO_BOUNDARY"
	}
	previousTime := start
	lostMessages := regexp.MustCompile(`(?:suppressed|dropped|lost|missed)\s+[0-9]+\s+(?:[a-z]+\s+)?messages`)
	for _, entry := range entries {
		when := timestamp(entry)
		if when < start || when > end || when < previousTime {
			return false, "INVALID_AUDIT_TIME"
		}
		previousTime = when
		message := strings.ToLower(entry.Message)
		if lostMessages.MatchString(message) {
			return false, "MESSAGES_LOST"
		}
		if reason := consume(entry); reason != "" {
			return false, reason
		}
	}
	if len(entries) >= 10000 {
		return false, "AUDIT_TRUNCATED"
	}
	return true, "CONTIGUOUS_SEQUENCE"
}
