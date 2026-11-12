package modelinfo

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
)

// parseOBJ 流式读文本：v 行统计顶点与包围盒，f 行按多边形三角化（n 边形 → n-2 个三角面）。
func parseOBJ(path string) (Metrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metrics{}, err
	}
	defer f.Close()

	low := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	high := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}

	vertices, triangles := 0, 0
	hasVertex := false
	groups := map[string]struct{}{}
	materials := map[string]struct{}{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "v "):
			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}
			point := [3]float64{0, 0, 0}
			ok := true
			for axis := 0; axis < 3; axis++ {
				value, parseErr := strconv.ParseFloat(fields[axis+1], 64)
				if parseErr != nil || math.IsNaN(value) || math.IsInf(value, 0) {
					ok = false
					break
				}
				point[axis] = value
			}
			if !ok {
				continue
			}
			vertices++
			hasVertex = true
			for axis := 0; axis < 3; axis++ {
				if point[axis] < low[axis] {
					low[axis] = point[axis]
				}
				if point[axis] > high[axis] {
					high[axis] = point[axis]
				}
			}
		case strings.HasPrefix(line, "f "):
			count := len(strings.Fields(line)) - 1
			if count >= 3 {
				triangles += count - 2
			}
		case strings.HasPrefix(line, "g "), strings.HasPrefix(line, "o "):
			if name := strings.TrimSpace(line[2:]); name != "" {
				groups[name] = struct{}{}
			}
		case strings.HasPrefix(line, "usemtl "):
			if name := strings.TrimSpace(line[7:]); name != "" {
				materials[name] = struct{}{}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Metrics{}, err
	}

	metrics := Metrics{Vertices: vertices, Triangles: triangles}
	if vertices > 0 {
		metrics.Meshes = max(1, len(groups))
		metrics.Materials = max(1, len(materials))
		metrics.Dimensions = sizeOf(low, high, hasVertex)
	}
	return metrics, nil
}
