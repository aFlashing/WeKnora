package convert

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/infrastructure/chm2epub/logx"
)

// Result 是批量转换中单本的执行结果。
type Result struct {
	Input  string
	Output string
	Err    error
}

// Batch 转换目录下的所有 .chm。
func Batch(dir string, recursive bool, opt Options, workers int) ([]Result, error) {
	files, err := CollectCHM(dir, recursive)
	if err != nil {
		return nil, err
	}
	return RunBatch(files, opt, workers), nil
}

// CollectCHM 收集目录下的 .chm 文件（可按需递归）。
func CollectCHM(dir string, recursive bool) ([]string, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return []string{dir}, nil
	}

	var files []string
	if recursive {
		err = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.EqualFold(filepath.Ext(p), ".chm") {
				files = append(files, p)
			}
			return nil
		})
	} else {
		entries, derr := os.ReadDir(dir)
		if derr != nil {
			return nil, derr
		}
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".chm") {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// RunBatch 并发转换给定的 CHM 列表。
func RunBatch(files []string, opt Options, workers int) []Result {
	if workers < 1 {
		workers = 1
	}
	if workers > len(files) {
		workers = len(files)
	}
	if workers == 0 {
		return nil
	}

	results := make([]Result, len(files))
	jobs := make(chan int)
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				in := files[idx]
				out, err := One(in, opt)
				results[idx] = Result{Input: in, Output: out, Err: err}
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

// Summarize 输出批量结果统计，返回失败数量。
func Summarize(results []Result, log *logx.Logger) int {
	failed := 0
	for _, r := range results {
		name := filepath.Base(r.Input)
		if r.Err != nil {
			failed++
			log.Errorf("%s: %v", name, r.Err)
			continue
		}
		log.Infof("%s -> %s", name, filepath.Base(r.Output))
	}
	fmt.Printf("完成: 成功 %d, 失败 %d, 共 %d\n", len(results)-failed, failed, len(results))
	return failed
}
