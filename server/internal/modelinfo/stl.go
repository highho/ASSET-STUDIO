package modelinfo

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

// parseSTL 先按「84 字节头 + n×50 字节」判断是不是二进制 STL，不是就按 ASCII 文本读。
func parseSTL(path string) (Metrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metrics{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return Metrics{}, err
	}

	head := make([]byte, 84)
	if read, readErr := io.ReadFull(f, head); readErr == nil && read == 84 {
		count := binary.LittleEndian.Uint32(head[80:84])
		// 文件大小必须刚好对得上，否则当作 ASCII（有些 ASCII 文件的头部碰巧也够 84 字节）
		if count > 0 && int64(84)+int64(count)*50 == info.Size() {
			return parseBinarySTL(f, int(count)), nil
		}
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Metrics{}, err
	}
	return parseASCIISTL(f)
}

func parseBinarySTL(f *os.File, count int) Metrics {
	low := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	high := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	record := make([]byte, 50)
	for i := 0; i < count; i++ {
		if _, err := io.ReadFull(f, record); err != nil {
			// 记录读到一半就断了：按已读到的三角形计数，不整体失败
			count = i
			break
		}
		for vertex := 3; vertex < 12; vertex += 3 {
			for axis := 0; axis < 3; axis++ {
				value := float64(math.Float32frombits(binary.LittleEndian.Uint32(record[vertex*4+axis*4 : vertex*4+axis*4+4])))
				if value < low[axis] {
					low[axis] = value
				}
				if value > high[axis] {
					high[axis] = value
				}
			}
		}
	}
	// 与 three.js 的 STLLoader 一致：STL 没有索引，顶点数是三角面数的 3 倍。
	return Metrics{
		Vertices:   3 * count,
		Triangles:  count,
		Meshes:     1,
		Materials:  1,
		Dimensions: sizeOf(low, high, count > 0),
	}
}

func parseASCIISTL(f *os.File) (Metrics, error) {
	low := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	high := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	facets, vertices := 0, 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "facet"):
			facets++
		case strings.HasPrefix(line, "vertex"):
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
			for axis := 0; axis < 3; axis++ {
				if point[axis] < low[axis] {
					low[axis] = point[axis]
				}
				if point[axis] > high[axis] {
					high[axis] = point[axis]
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Metrics{}, err
	}
	if facets == 0 {
		return Metrics{}, fmt.Errorf("不是可识别的 STL 文件（二进制与 ASCII 都没解析出三角面）")
	}
	return Metrics{
		Vertices:   vertices,
		Triangles:  facets,
		Meshes:     1,
		Materials:  1,
		Dimensions: sizeOf(low, high, vertices > 0),
	}, nil
}
