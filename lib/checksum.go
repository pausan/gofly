// Copyright (C) 2026 Pau Sanchez
//
// Flyway-compatible checksum calculation.
//
// Flyway computes a CRC-32 over the migration contents read line by line, which
// makes the result independent of the line endings and of the trailing newline.
// The BOM, when present, is stripped from the first line only.
//
// See org.flywaydb.core.internal.resolver.ChecksumCalculator
package lib

import (
	"bufio"
	"hash/crc32"
	"io"
	"os"
	"strings"
)

// utf8Bom is the byte order mark that Flyway's BomFilter removes
const utf8Bom = '\ufeff'

// -----------------------------------------------------------------------------
// ChecksumBytes
//
// Calculates the Flyway checksum of an in-memory migration.
// -----------------------------------------------------------------------------
func ChecksumBytes(content []byte) int32 {
	return checksumFromReader(strings.NewReader(string(content)))
}

// -----------------------------------------------------------------------------
// ChecksumString
// -----------------------------------------------------------------------------
func ChecksumString(content string) int32 {
	return checksumFromReader(strings.NewReader(content))
}

// -----------------------------------------------------------------------------
// ChecksumFile
//
// Calculates the Flyway checksum of a migration stored on disk.
// -----------------------------------------------------------------------------
func ChecksumFile(path string) (int32, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	return checksumFromReader(file), nil
}

// -----------------------------------------------------------------------------
// ChecksumCombine
//
// When a migration is made of several resources Flyway CRC-32's the big-endian
// representation of each individual checksum. A single resource keeps its own
// checksum untouched.
// -----------------------------------------------------------------------------
func ChecksumCombine(checksums []int32) int32 {
	if len(checksums) == 1 {
		return checksums[0]
	}

	digest := crc32.NewIEEE()
	for _, checksum := range checksums {
		value := uint32(checksum)
		digest.Write([]byte{
			byte(value >> 24),
			byte(value >> 16),
			byte(value >> 8),
			byte(value),
		})
	}

	return int32(digest.Sum32())
}

// -----------------------------------------------------------------------------
// checksumFromReader
//
// Reads line by line the same way java.io.BufferedReader.readLine does: a line
// is terminated by "\n", "\r\n" or a lone "\r", and the terminator is never fed
// to the CRC.
// -----------------------------------------------------------------------------
func checksumFromReader(reader io.Reader) int32 {
	digest := crc32.NewIEEE()
	scanner := bufio.NewReader(reader)

	firstLine := true
	for {
		line, err := readJavaLine(scanner)
		if line == nil {
			break
		}

		text := *line
		if firstLine {
			text = stripBom(text)
			firstLine = false
		}

		digest.Write([]byte(text))

		if err != nil {
			break
		}
	}

	return int32(digest.Sum32())
}

// -----------------------------------------------------------------------------
// readJavaLine
//
// Returns the next line without its terminator, or nil once the input is over.
// The returned error is io.EOF when the stream ended with this very line.
// -----------------------------------------------------------------------------
func readJavaLine(reader *bufio.Reader) (*string, error) {
	line := strings.Builder{}
	readAnything := false

	for {
		char, _, err := reader.ReadRune()
		if err != nil {
			if !readAnything {
				return nil, io.EOF
			}
			result := line.String()
			return &result, io.EOF
		}

		readAnything = true

		if char == '\n' {
			result := line.String()
			return &result, nil
		}

		if char == '\r' {
			// swallow the '\n' of a "\r\n" pair, a lone '\r' also ends the line
			next, _, err := reader.ReadRune()
			if err == nil && next != '\n' {
				reader.UnreadRune()
			}
			result := line.String()
			return &result, nil
		}

		line.WriteRune(char)
	}
}

// -----------------------------------------------------------------------------
// stripBom
// -----------------------------------------------------------------------------
func stripBom(line string) string {
	runes := []rune(line)
	if len(runes) > 0 && runes[0] == utf8Bom {
		return string(runes[1:])
	}
	return line
}
