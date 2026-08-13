package codexitem

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/ai-coding-remote/mac-agent/internal/protocol"
)

type RawItem struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	Phase            string          `json:"phase"`
	Status           string          `json:"status"`
	Command          string          `json:"command"`
	CWD              string          `json:"cwd"`
	AggregatedOutput string          `json:"aggregatedOutput"`
	ExitCode         *int            `json:"exitCode"`
	DurationMS       *int64          `json:"durationMs"`
	Tool             string          `json:"tool"`
	Server           string          `json:"server"`
	Prompt           string          `json:"prompt"`
	Query            string          `json:"query"`
	Path             string          `json:"path"`
	SavedPath        string          `json:"savedPath"`
	Result           string          `json:"result"`
	Review           string          `json:"review"`
	Summary          []string        `json:"summary"`
	Content          json.RawMessage `json:"content"`
	Changes          []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
		Diff string `json:"diff"`
	} `json:"changes"`
}

func Normalize(raw json.RawMessage, fieldLimit int) (protocol.ThreadHistoryItem, bool) {
	var source RawItem
	if json.Unmarshal(raw, &source) != nil || source.ID == "" || source.Type == "" {
		return protocol.ThreadHistoryItem{}, false
	}
	return NormalizeRaw(source, fieldLimit), true
}

func NormalizeRaw(source RawItem, fieldLimit int) protocol.ThreadHistoryItem {
	item := protocol.ThreadHistoryItem{
		ID: source.ID, Type: source.Type, Phase: source.Phase, Status: source.Status,
		Command: source.Command, CWD: source.CWD, Output: source.AggregatedOutput,
		ExitCode: source.ExitCode, DurationMS: source.DurationMS, Query: source.Query, Path: source.Path,
	}
	switch source.Type {
	case "userMessage":
		item.Role = "user"
		var contents []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Path string `json:"path"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(source.Content, &contents)
		var parts []string
		for _, content := range contents {
			switch content.Type {
			case "text":
				parts = append(parts, content.Text)
			case "localImage", "localAudio", "skill", "mention":
				label := content.Name
				if label == "" {
					label = filepath.Base(content.Path)
				}
				parts = append(parts, "["+content.Type+": "+label+"]")
			case "image", "audio":
				parts = append(parts, "["+content.Type+"]")
			}
		}
		item.Text = strings.Join(parts, "\n")
	case "agentMessage":
		item.Role = "assistant"
		item.Text = source.Text
	case "plan":
		item.Role = "assistant"
		item.Text = source.Text
	case "reasoning":
		item.Role = "assistant"
		var content []string
		_ = json.Unmarshal(source.Content, &content)
		item.Text = strings.Join(append(source.Summary, content...), "\n")
	case "mcpToolCall":
		item.Name = strings.Trim(source.Server+"/"+source.Tool, "/")
	case "dynamicToolCall", "collabAgentToolCall":
		item.Name = source.Tool
		item.Text = source.Prompt
	case "imageView":
		item.Path = source.Path
	case "imageGeneration":
		item.Path = source.SavedPath
		item.Text = source.Result
	case "enteredReviewMode", "exitedReviewMode":
		item.Text = source.Review
	}
	for _, change := range source.Changes {
		item.Changes = append(item.Changes, protocol.ThreadFileChange{Path: change.Path, Kind: change.Kind, Diff: change.Diff})
	}
	item.Text, item.Truncated = truncateUTF8(item.Text, fieldLimit)
	var clipped bool
	fields := []*string{&item.Name, &item.Command, &item.CWD, &item.Output, &item.Path, &item.Query}
	for _, field := range fields {
		*field, clipped = truncateUTF8(*field, fieldLimit)
		item.Truncated = item.Truncated || clipped
	}
	for index := range item.Changes {
		item.Changes[index].Path, clipped = truncateUTF8(item.Changes[index].Path, fieldLimit)
		item.Truncated = item.Truncated || clipped
		item.Changes[index].Diff, clipped = truncateUTF8(item.Changes[index].Diff, fieldLimit)
		item.Truncated = item.Truncated || clipped
	}
	return item
}

func truncateUTF8(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	const suffix = "..."
	end := limit - len(suffix)
	if end < 0 {
		end = 0
	}
	for end > 0 && end < len(value) && (value[end]&0xc0) == 0x80 {
		end--
	}
	return value[:end] + suffix, true
}
