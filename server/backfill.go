package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"

	"assetstudio/server/internal/modelinfo"
	"assetstudio/server/internal/store"
)

// runBackfill 补齐老数据的服务端指标与 sha256，然后退出。
// 典型场景：库里那批「三角面为 0」的历史资产（扫描时没有几何、只等浏览器补的那种）。
func runBackfill(st *store.Store) {
	ctx := context.Background()
	assets, err := st.List(ctx)
	if err != nil {
		log.Printf("读取资产列表失败: %v", err)
		return
	}

	filled, skipped, failed := 0, 0, 0
	for index := range assets {
		asset := &assets[index]
		needMetrics := !(asset.MetricsSource == "server" && asset.MetricsState == "ok")
		needHash := asset.SHA256 == ""
		if !needMetrics && !needHash {
			continue
		}

		abs := filepath.Join(st.DataDir, asset.FilePath)
		if _, statErr := os.Stat(abs); statErr != nil {
			skipped++
			log.Printf("  跳过 %s：源文件不存在（%s）", asset.Name, asset.FilePath)
			continue
		}

		digest := asset.SHA256
		if needHash {
			sum, hashErr := hashFile(abs)
			if hashErr != nil {
				failed++
				log.Printf("  失败 %s：计算 sha256 出错 %v", asset.Name, hashErr)
				continue
			}
			digest = sum
		}

		parsed, parseErr := modelinfo.Parse(abs, asset.Format)
		if errors.Is(parseErr, modelinfo.ErrUnsupported) {
			skipped++
			continue
		}
		if parseErr != nil {
			failed++
			_ = st.SetMetricsError(ctx, asset.ID, parseErr.Error())
			log.Printf("  失败 %s：%v", asset.Name, parseErr)
			continue
		}

		stats := store.Stats{
			Vertices:   parsed.Vertices,
			Triangles:  parsed.Triangles,
			Meshes:     parsed.Meshes,
			Materials:  parsed.Materials,
			Dimensions: parsed.Dimensions,
		}
		if err := st.SetServerMetrics(ctx, asset.ID, stats, digest); err != nil {
			failed++
			log.Printf("  失败 %s：写库出错 %v", asset.Name, err)
			continue
		}
		filled++
	}
	log.Printf("补齐完成：成功 %d 条、跳过 %d 条、失败 %d 条", filled, skipped, failed)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
