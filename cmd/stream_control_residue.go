package cmd

import (
	"strings"
	"unicode/utf8"
)

const streamControlResidueMinFragmentLength = 3

var streamControlResidueMarkers = []string{"FINALIZE"}

type streamControlResidueState struct {
	pending       string
	lastRune      rune
	hasLastOutput bool
}

func (s *streamControlResidueState) consume(chunk string) string {
	if chunk == "" {
		return ""
	}
	input := s.pending + chunk
	s.pending = ""
	output, pending := splitTrailingStreamControlResidue(input, s.lastRune, s.hasLastOutput)
	s.pending = pending
	s.rememberOutput(output)
	return output
}

func (s *streamControlResidueState) flush() {
	s.pending = ""
}

func (s *streamControlResidueState) rememberOutput(content string) {
	if content == "" {
		return
	}
	lastRune, _ := utf8.DecodeLastRuneInString(content)
	if lastRune == utf8.RuneError {
		return
	}
	s.lastRune = lastRune
	s.hasLastOutput = true
}

func splitTrailingStreamControlResidue(input string, previousRune rune, hasPreviousRune bool) (string, string) {
	if input == "" {
		return "", ""
	}
	for _, marker := range streamControlResidueMarkers {
		if strings.HasSuffix(input, marker) {
			start := len(input) - len(marker)
			if hasStreamControlResidueBoundary(input, start, previousRune, hasPreviousRune) {
				return input[:start], ""
			}
		}
	}
	for _, marker := range streamControlResidueMarkers {
		for length := len(marker) - 1; length >= streamControlResidueMinFragmentLength; length-- {
			prefixFragment := marker[:length]
			if strings.HasSuffix(input, prefixFragment) {
				start := len(input) - len(prefixFragment)
				if hasStreamControlResidueBoundary(input, start, previousRune, hasPreviousRune) {
					return input[:start], prefixFragment
				}
			}

			suffixFragment := marker[len(marker)-length:]
			if suffixFragment == prefixFragment || !strings.HasSuffix(input, suffixFragment) {
				continue
			}
			start := len(input) - len(suffixFragment)
			if hasStreamControlResidueBoundary(input, start, previousRune, hasPreviousRune) {
				return input[:start], suffixFragment
			}
		}
	}
	return input, ""
}

func hasStreamControlResidueBoundary(input string, start int, previousRune rune, hasPreviousRune bool) bool {
	if start > 0 {
		candidatePrefix := input[:start]
		lastRune, _ := utf8.DecodeLastRuneInString(candidatePrefix)
		return !isASCIIWordRune(lastRune)
	}
	if hasPreviousRune {
		return !isASCIIWordRune(previousRune)
	}
	return true
}

func isASCIIWordRune(value rune) bool {
	return value == '_' || (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9')
}
