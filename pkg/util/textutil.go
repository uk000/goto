package util

import (
	"sort"
	"strings"
)

func SplitTextIntoChunks(text string, chunkSize int) []string {
	// If text is short enough, return it as a single chunk
	if len(text) <= chunkSize {
		return []string{text}
	}

	// Split text by words to ensure we don't break words
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}

	chunks := []string{}
	currentChunk := ""

	for _, word := range words {
		// Check if adding this word would exceed the target chunk size
		if len(currentChunk) > 0 && len(currentChunk)+len(word)+1 > chunkSize && len(currentChunk) > 0 {
			// Current chunk is full, add it to the list
			chunks = append(chunks, currentChunk)
			currentChunk = word
		} else {
			// Add word to current chunk with a space if needed
			if len(currentChunk) > 0 {
				currentChunk += " "
			}
			currentChunk += word
		}
	}

	// Add the last chunk if not empty
	if len(currentChunk) > 0 {
		chunks = append(chunks, currentChunk)
	}

	// If we have very few chunks or they're very uneven, try a more balanced approach
	if len(chunks) < 3 && len(text) > 15 {
		// Find sentence boundaries or reasonable splitting points
		return SplitAtSentenceBoundaries(text, 3)
	}

	return chunks
}
func SplitAtSentenceBoundaries(text string, targetChunks int) []string {
	// Common sentence delimiters
	delimiters := []string{". ", "! ", "? ", "\n\n", "; "}

	// If text is small, don't try to split it too much
	if len(text) < 30 {
		return []string{text}
	}

	// Find all potential split points
	var splitPoints []int
	for _, delimiter := range delimiters {
		idx := 0
		for {
			found := strings.Index(text[idx:], delimiter)
			if found == -1 {
				break
			}
			// Add the position after the delimiter
			splitPoint := idx + found + len(delimiter)
			splitPoints = append(splitPoints, splitPoint)
			idx = splitPoint
		}
	}

	// Sort split points
	sort.Ints(splitPoints)

	// If no good split points found, fall back to even division
	if len(splitPoints) < targetChunks-1 {
		chunkSize := len(text) / targetChunks
		chunks := make([]string, targetChunks)
		for i := 0; i < targetChunks-1; i++ {
			chunks[i] = text[i*chunkSize : (i+1)*chunkSize]
		}
		chunks[targetChunks-1] = text[(targetChunks-1)*chunkSize:]
		return chunks
	}

	// Select evenly spaced split points
	selectedPoints := make([]int, targetChunks-1)
	step := len(splitPoints) / targetChunks
	for i := 0; i < targetChunks-1; i++ {
		index := min((i+1)*step, len(splitPoints)-1)
		selectedPoints[i] = splitPoints[index]
	}
	sort.Ints(selectedPoints)

	// Create chunks based on selected split points
	chunks := make([]string, targetChunks)
	startIdx := 0
	for i, point := range selectedPoints {
		chunks[i] = text[startIdx:point]
		startIdx = point
	}
	chunks[targetChunks-1] = text[startIdx:]

	return chunks
}
func ReverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
