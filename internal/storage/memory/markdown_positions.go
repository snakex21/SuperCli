package memory

import (
	"bufio"
	"fmt"
	"os"
)

type markdownPosition struct {
	ID                 string
	LineStart, LineEnd int
}

// mdReadPositions follows mdRead's header and line boundaries without retaining
// entry contents. Mirror verification only uses IDs and persisted line offsets;
// the full reader remains available to imports and content callers.
func mdReadPositions(path string) ([]markdownPosition, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory.mdRead: open %s: %w", path, err)
	}
	defer f.Close()
	var positions []markdownPosition
	var lineNo int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineNo++
		if match := mdHeader.FindSubmatch(scanner.Bytes()); match != nil {
			if len(positions) > 0 {
				positions[len(positions)-1].LineEnd = lineNo - 1
			}
			positions = append(positions, markdownPosition{ID: string(match[1]), LineStart: lineNo})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("memory.mdRead: scan %s: %w", path, err)
	}
	if len(positions) > 0 {
		positions[len(positions)-1].LineEnd = lineNo
	}
	return positions, nil
}
