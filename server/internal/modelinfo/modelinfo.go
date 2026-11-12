// Package modelinfo 在服务端直接解析模型文件，算出「硬指标」：顶点、三角面、网格数、
// 材质数与包围盒尺寸。目的是让几何真相落在服务端，而不是只存在浏览器里。
// 当前支持 GLB / GLTF / OBJ / STL；FBX 仍由浏览器解析（ErrUnsupported）。
package modelinfo

import (
	"errors"
	"strings"
)

// ErrUnsupported 表示服务端不解析这种格式，调用方应把指标留给浏览器。
var ErrUnsupported = errors.New("服务端不解析该格式")

// Metrics 与前端 store.Stats 的结构对齐，方便直接落库。
type Metrics struct {
	Vertices   int        `json:"vertices"`
	Triangles  int        `json:"triangles"`
	Meshes     int        `json:"meshes"`
	Materials  int        `json:"materials"`
	Dimensions [3]float64 `json:"dimensions"`
}

// Parse 按格式从磁盘文件算指标；format 是小写扩展名。
func Parse(path, format string) (Metrics, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "glb":
		return parseGLB(path)
	case "gltf":
		return parseGLTF(path)
	case "obj":
		return parseOBJ(path)
	case "stl":
		return parseSTL(path)
	default:
		return Metrics{}, ErrUnsupported
	}
}
