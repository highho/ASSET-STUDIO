package modelinfo

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

const (
	glbMagic     = 0x46546C67 // "glTF"
	glbChunkJSON = 0x4E4F534A // "JSON"
	maxJSONBytes = 64 << 20   // 单个 glTF JSON 分块的读取上限
)

type gltfAccessor struct {
	Count int       `json:"count"`
	Type  string    `json:"type"`
	Min   []float64 `json:"min"`
	Max   []float64 `json:"max"`
}

type gltfPrimitive struct {
	Attributes map[string]int `json:"attributes"`
	Indices    *int           `json:"indices"`
	Material   *int           `json:"material"`
	Mode       *int           `json:"mode"`
}

type gltfMesh struct {
	Primitives []gltfPrimitive `json:"primitives"`
}

type gltfNode struct {
	Mesh        *int      `json:"mesh"`
	Matrix      []float64 `json:"matrix"`
	Translation []float64 `json:"translation"`
	Rotation    []float64 `json:"rotation"`
	Scale       []float64 `json:"scale"`
	Children    []int     `json:"children"`
}

type gltfDoc struct {
	Accessors []gltfAccessor    `json:"accessors"`
	Meshes    []gltfMesh        `json:"meshes"`
	Materials []json.RawMessage `json:"materials"`
	Nodes     []gltfNode        `json:"nodes"`
}

func parseGLTF(path string) (Metrics, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Metrics{}, err
	}
	if info.Size() > maxJSONBytes {
		return Metrics{}, fmt.Errorf("glTF JSON 过大（%d 字节），跳过服务端解析", info.Size())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Metrics{}, err
	}
	var doc gltfDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Metrics{}, fmt.Errorf("glTF JSON 解析失败: %w", err)
	}
	return summarizeGLTF(&doc), nil
}

// parseGLB 只读文件头与 JSON 分块，二进制数据块整块跳过，所以大模型也不会占内存。
func parseGLB(path string) (Metrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metrics{}, err
	}
	defer f.Close()

	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return Metrics{}, fmt.Errorf("GLB 头部不完整: %w", err)
	}
	if binary.LittleEndian.Uint32(header[0:4]) != glbMagic {
		return Metrics{}, errors.New("不是 GLB 文件（magic 不为 glTF）")
	}
	if version := binary.LittleEndian.Uint32(header[4:8]); version != 2 {
		return Metrics{}, fmt.Errorf("不支持的 GLB 版本 %d", version)
	}

	for {
		chunkHeader := make([]byte, 8)
		if _, err := io.ReadFull(f, chunkHeader); err != nil {
			return Metrics{}, fmt.Errorf("GLB 里没有找到 JSON 分块: %w", err)
		}
		length := binary.LittleEndian.Uint32(chunkHeader[0:4])
		kind := binary.LittleEndian.Uint32(chunkHeader[4:8])
		if kind != glbChunkJSON {
			if _, err := f.Seek(int64(length), io.SeekCurrent); err != nil {
				return Metrics{}, fmt.Errorf("跳过 GLB 分块失败: %w", err)
			}
			continue
		}
		if length > maxJSONBytes {
			return Metrics{}, fmt.Errorf("GLB 的 JSON 分块过大（%d 字节）", length)
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(f, buf); err != nil {
			return Metrics{}, fmt.Errorf("读取 GLB JSON 分块失败: %w", err)
		}
		var doc gltfDoc
		if err := json.Unmarshal(buf, &doc); err != nil {
			return Metrics{}, fmt.Errorf("GLB 内 JSON 解析失败: %w", err)
		}
		return summarizeGLTF(&doc), nil
	}
}

func summarizeGLTF(doc *gltfDoc) Metrics {
	var m Metrics
	usedMaterials := map[int]bool{}
	primitives := 0

	for _, mesh := range doc.Meshes {
		for _, prim := range mesh.Primitives {
			primitives++
			posIndex, ok := prim.Attributes["POSITION"]
			if !ok || posIndex < 0 || posIndex >= len(doc.Accessors) {
				continue
			}
			positionCount := doc.Accessors[posIndex].Count
			m.Vertices += positionCount

			elements := positionCount
			if prim.Indices != nil && *prim.Indices >= 0 && *prim.Indices < len(doc.Accessors) {
				elements = doc.Accessors[*prim.Indices].Count
			}
			mode := 4 // 默认 TRIANGLES
			if prim.Mode != nil {
				mode = *prim.Mode
			}
			switch mode {
			case 4:
				m.Triangles += elements / 3
			case 5, 6: // TRIANGLE_STRIP / TRIANGLE_FAN
				if elements >= 3 {
					m.Triangles += elements - 2
				}
			}
			if prim.Material != nil {
				usedMaterials[*prim.Material] = true
			}
		}
	}

	m.Meshes = primitives
	if len(usedMaterials) > 0 {
		m.Materials = len(usedMaterials)
	} else {
		m.Materials = len(doc.Materials)
	}
	m.Dimensions = sceneBounds(doc)
	return m
}

// sceneBounds 按节点层级把每个网格的局部包围盒变换到世界坐标后取并集。
func sceneBounds(doc *gltfDoc) [3]float64 {
	low := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	high := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	found := false

	addPoint := func(p [3]float64) {
		for i := 0; i < 3; i++ {
			if p[i] < low[i] {
				low[i] = p[i]
			}
			if p[i] > high[i] {
				high[i] = p[i]
			}
		}
		found = true
	}

	addMesh := func(world mat4, meshIndex int) {
		if meshIndex < 0 || meshIndex >= len(doc.Meshes) {
			return
		}
		for _, prim := range doc.Meshes[meshIndex].Primitives {
			posIndex, ok := prim.Attributes["POSITION"]
			if !ok || posIndex < 0 || posIndex >= len(doc.Accessors) {
				continue
			}
			accessor := doc.Accessors[posIndex]
			// POSITION 访问器的 min/max 在 glTF 里是必填项，缺失就放弃这一块的包围盒。
			if len(accessor.Min) < 3 || len(accessor.Max) < 3 {
				continue
			}
			for _, corner := range boxCorners(accessor.Min, accessor.Max) {
				addPoint(world.point(corner[0], corner[1], corner[2]))
			}
		}
	}

	if len(doc.Nodes) == 0 {
		for index := range doc.Meshes {
			addMesh(identity(), index)
		}
		return sizeOf(low, high, found)
	}

	isChild := make(map[int]bool, len(doc.Nodes))
	for _, node := range doc.Nodes {
		for _, child := range node.Children {
			isChild[child] = true
		}
	}
	roots := make([]int, 0, len(doc.Nodes))
	for index := range doc.Nodes {
		if !isChild[index] {
			roots = append(roots, index)
		}
	}
	if len(roots) == 0 { // 数据有环等异常时退化为「所有节点都当根」
		for index := range doc.Nodes {
			roots = append(roots, index)
		}
	}

	var walk func(index int, parent mat4)
	walk = func(index int, parent mat4) {
		if index < 0 || index >= len(doc.Nodes) {
			return
		}
		node := doc.Nodes[index]
		world := parent.mul(nodeMatrix(node))
		if node.Mesh != nil {
			addMesh(world, *node.Mesh)
		}
		for _, child := range node.Children {
			walk(child, world)
		}
	}
	for _, root := range roots {
		walk(root, identity())
	}
	return sizeOf(low, high, found)
}

func sizeOf(low, high [3]float64, found bool) [3]float64 {
	if !found {
		return [3]float64{}
	}
	return [3]float64{high[0] - low[0], high[1] - low[1], high[2] - low[2]}
}

func boxCorners(min, max []float64) [8][3]float64 {
	var corners [8][3]float64
	for i := 0; i < 8; i++ {
		for axis := 0; axis < 3; axis++ {
			if i&(1<<axis) != 0 {
				corners[i][axis] = max[axis]
			} else {
				corners[i][axis] = min[axis]
			}
		}
	}
	return corners
}

// mat4 是列主序 4×4 矩阵，与 glTF 的 matrix 字段一致。
type mat4 [16]float64

func identity() mat4 {
	return mat4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
}

func (m mat4) mul(n mat4) mat4 {
	var out mat4
	for column := 0; column < 4; column++ {
		for row := 0; row < 4; row++ {
			out[column*4+row] = m[row]*n[column*4] +
				m[4+row]*n[column*4+1] +
				m[8+row]*n[column*4+2] +
				m[12+row]*n[column*4+3]
		}
	}
	return out
}

func (m mat4) point(x, y, z float64) [3]float64 {
	return [3]float64{
		m[0]*x + m[4]*y + m[8]*z + m[12],
		m[1]*x + m[5]*y + m[9]*z + m[13],
		m[2]*x + m[6]*y + m[10]*z + m[14],
	}
}

func nodeMatrix(node gltfNode) mat4 {
	if len(node.Matrix) == 16 {
		var m mat4
		copy(m[:], node.Matrix)
		return m
	}
	translation := [3]float64{}
	if len(node.Translation) >= 3 {
		translation = [3]float64{node.Translation[0], node.Translation[1], node.Translation[2]}
	}
	scale := [3]float64{1, 1, 1}
	if len(node.Scale) >= 3 {
		scale = [3]float64{node.Scale[0], node.Scale[1], node.Scale[2]}
	}
	quat := [4]float64{0, 0, 0, 1} // x, y, z, w
	if len(node.Rotation) >= 4 {
		quat = [4]float64{node.Rotation[0], node.Rotation[1], node.Rotation[2], node.Rotation[3]}
	}
	x, y, z, w := quat[0], quat[1], quat[2], quat[3]
	xx, yy, zz := x*x, y*y, z*z
	xy, xz, yz := x*y, x*z, y*z
	wx, wy, wz := w*x, w*y, w*z

	return mat4{
		(1 - 2*(yy+zz)) * scale[0], (2 * (xy + wz)) * scale[0], (2 * (xz - wy)) * scale[0], 0,
		(2 * (xy - wz)) * scale[1], (1 - 2*(xx+zz)) * scale[1], (2 * (yz + wx)) * scale[1], 0,
		(2 * (xz + wy)) * scale[2], (2 * (yz - wx)) * scale[2], (1 - 2*(xx+yy)) * scale[2], 0,
		translation[0], translation[1], translation[2], 1,
	}
}
