package aiusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"

	"github.com/klauspost/compress/zstd"
)

// DeepSeek Harness (dsh) session transcripts:
// $DSH_HOME/sessions/**/session*.jsonl[.zstd] (default ~/.dsh/sessions/...).
//
// request/context lines set data.model; assistant/message lines carry time
// (Unix ms), seq, and data.usage.{inputTokens, outputTokens, cacheReadTokens,
// cacheWriteTokens}.

type dshLineJSON struct {
	Type string          `json:"type"`
	Seq  *int64          `json:"seq"`
	Time json.RawMessage `json:"time"`
	Data struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens      int64 `json:"inputTokens"`
			OutputTokens     int64 `json:"outputTokens"`
			CacheReadTokens  int64 `json:"cacheReadTokens"`
			CacheWriteTokens int64 `json:"cacheWriteTokens"`
		} `json:"usage"`
	} `json:"data"`
}

func parseDSHStream(path string, r io.Reader, pool map[string]*string) ([]rec, error) {
	sessionID := filepath.Base(filepath.Dir(path))
	var out []rec
	var curModel *string
	br := bufio.NewReaderSize(r, 256<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var e dshLineJSON
			if json.Unmarshal(line, &e) == nil {
				switch e.Type {
				case "request/context":
					if e.Data.Model != "" {
						curModel = intern(pool, e.Data.Model)
					}
				case "assistant/message":
					u := e.Data.Usage
					if u != nil && (u.InputTokens > 0 || u.OutputTokens > 0 || u.CacheReadTokens > 0 || u.CacheWriteTokens > 0) {
						if at := parseMillis(e.Time); at != 0 {
							var key string
							if e.Seq != nil {
								key = "dsh:" + sessionID + ":" + strconv.FormatInt(*e.Seq, 10)
							}
							out = append(out, rec{
								at:    at,
								model: curModel,
								in:    u.InputTokens,
								out:   u.OutputTokens,
								cr:    u.CacheReadTokens,
								cw:    u.CacheWriteTokens,
								key:   key,
							})
						}
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return out, err
		}
	}
	return out, nil
}

func parseDSHFile(path string, data []byte, pool map[string]*string) ([]rec, error) {
	return parseDSHStream(path, bytes.NewReader(data), pool)
}

func parseDSHZstdFile(path string, data []byte, pool map[string]*string) ([]rec, error) {
	zr, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return parseDSHStream(path, zr, pool)
}
