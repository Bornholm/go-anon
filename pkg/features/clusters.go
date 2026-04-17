package features

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

type BrownClusters struct {
	clusters map[string]string
}

func LoadBrownClusters(r io.Reader) (*BrownClusters, error) {
	bc := &BrownClusters{clusters: make(map[string]string)}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			cluster := parts[0]
			word := strings.ToLower(parts[1])
			bc.clusters[word] = cluster
		}
	}
	return bc, scanner.Err()
}

func (bc *BrownClusters) Cluster(word string) string {
	return bc.clusters[strings.ToLower(word)]
}

func (bc *BrownClusters) Prefixes(word string) map[string]string {
	c := bc.Cluster(word)
	if c == "" {
		return nil
	}
	result := make(map[string]string)
	for _, prefLen := range []int{4, 6, 10, 20} {
		if prefLen <= len(c) {
			result[fmt.Sprintf("brown%d", prefLen)] = c[:prefLen]
		}
	}
	return result
}
