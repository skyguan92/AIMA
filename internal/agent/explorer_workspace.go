package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// readOnlyDocs lists AIMA-generated fact documents the Explorer agent must not overwrite.
var readOnlyDocs = map[string]bool{
	"device-profile.md":  true,
	"available-combos.md": true,
	"knowledge-base.md":  true,
}

// ExplorerWorkspace manages the file workspace for an Explorer session.
// It enforces path safety (no directory escape) and read-only guards on
// AIMA-generated fact documents.
type ExplorerWorkspace struct {
	root string
}

// NewExplorerWorkspace creates a workspace rooted at root.
func NewExplorerWorkspace(root string) *ExplorerWorkspace {
	return &ExplorerWorkspace{root: root}
}

// Init creates the workspace directory structure.
func (w *ExplorerWorkspace) Init() error {
	if err := os.MkdirAll(filepath.Join(w.root, "experiments"), 0755); err != nil {
		return fmt.Errorf("init workspace: %w", err)
	}
	return nil
}

// safePath resolves a relative path inside the workspace root and blocks escapes.
func (w *ExplorerWorkspace) safePath(rel string) (string, error) {
	abs := filepath.Join(w.root, filepath.FromSlash(rel))
	abs, err := filepath.Abs(abs)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	rootAbs, err := filepath.Abs(w.root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	// Ensure abs is within root (must have root as prefix followed by separator or equal)
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace root", rel)
	}
	return abs, nil
}

// ReadFile reads a file relative to the workspace root.
func (w *ExplorerWorkspace) ReadFile(rel string) (string, error) {
	p, err := w.safePath(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", rel, err)
	}
	return string(data), nil
}

// WriteFile writes content to a file relative to the workspace root.
// Blocks writes to read-only AIMA fact documents.
func (w *ExplorerWorkspace) WriteFile(rel, content string) error {
	if readOnlyDocs[filepath.Base(rel)] {
		return fmt.Errorf("%s is a read-only AIMA fact document", rel)
	}
	return w.writeFactDocument(rel, content)
}

// writeFactDocument writes content bypassing the read-only guard.
// Used internally for AIMA-generated fact documents.
func (w *ExplorerWorkspace) writeFactDocument(rel, content string) error {
	p, err := w.safePath(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}

// AppendFile appends content to a file relative to the workspace root.
// Blocks appends to read-only AIMA fact documents.
func (w *ExplorerWorkspace) AppendFile(rel, content string) error {
	if readOnlyDocs[filepath.Base(rel)] {
		return fmt.Errorf("%s is a read-only AIMA fact document", rel)
	}
	p, err := w.safePath(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", rel, err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open %s for append: %w", rel, err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("append %s: %w", rel, err)
	}
	return nil
}

// ListDir lists directory entries relative to the workspace root.
// Directories get a "/" suffix.
func (w *ExplorerWorkspace) ListDir(rel string) ([]string, error) {
	p, err := w.safePath(rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", rel, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	return names, nil
}

// GrepFile searches for pattern in a single file, returning "linenum:line" matches.
func (w *ExplorerWorkspace) GrepFile(pattern, rel string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern %q: %w", pattern, err)
	}
	p, err := w.safePath(rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", rel, err)
	}
	defer f.Close()
	var results []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			results = append(results, fmt.Sprintf("%d:%s", lineNum, line))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", rel, err)
	}
	return results, nil
}

// GrepDir searches for pattern across all files in a directory,
// returning "filename:linenum:line" matches.
func (w *ExplorerWorkspace) GrepDir(pattern, rel string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern %q: %w", pattern, err)
	}
	p, err := w.safePath(rel)
	if err != nil {
		return nil, err
	}
	var results []string
	err = filepath.WalkDir(p, func(path string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		f, err := os.Open(path)
		if err != nil {
			return nil // skip unreadable files
		}
		defer f.Close()
		rel, _ := filepath.Rel(p, path)
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				results = append(results, fmt.Sprintf("%s:%d:%s", rel, lineNum, line))
			}
		}
		return scanner.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("grep dir %s: %w", rel, err)
	}
	return results, nil
}

// yamlBlockRe matches a fenced yaml code block.
var yamlBlockRe = regexp.MustCompile("(?s)```ya?ml\n(.*?)```")

// parsePlanTasks extracts TaskSpec list from plan.md markdown.
// Looks for the yaml code block under "## Tasks".
func parsePlanTasks(md string) ([]TaskSpec, error) {
	section := extractSection(md, "## Tasks")
	if section == "" {
		return nil, fmt.Errorf("no ## Tasks section found")
	}
	matches := yamlBlockRe.FindStringSubmatch(section)
	if len(matches) < 2 {
		return nil, fmt.Errorf("no yaml code block in ## Tasks section")
	}
	var tasks []TaskSpec
	if err := yaml.Unmarshal([]byte(matches[1]), &tasks); err != nil {
		return nil, fmt.Errorf("parse tasks yaml: %w", err)
	}
	return tasks, nil
}

// parseRecommendedConfigs extracts RecommendedConfig list from summary.md.
// Looks for the yaml code block under "## Recommended Configurations".
func parseRecommendedConfigs(md string) ([]RecommendedConfig, error) {
	section := extractSection(md, "## Recommended Configurations")
	if section == "" {
		return nil, nil // no recommendations yet is normal
	}
	matches := yamlBlockRe.FindStringSubmatch(section)
	if len(matches) < 2 {
		return nil, nil
	}
	var configs []RecommendedConfig
	if err := yaml.Unmarshal([]byte(matches[1]), &configs); err != nil {
		return nil, fmt.Errorf("parse recommendations yaml: %w", err)
	}
	return configs, nil
}

// extractSection returns the content from a markdown heading until the next
// heading of equal or higher level (or end of document).
func extractSection(md, heading string) string {
	level := len(heading) - len(strings.TrimLeft(heading, "#"))
	idx := strings.Index(md, heading)
	if idx == -1 {
		return ""
	}
	rest := md[idx+len(heading):]
	// Find next heading of same or higher level
	prefix := strings.Repeat("#", level) + " "
	for i := 0; i < len(rest); i++ {
		if i == 0 || rest[i-1] == '\n' {
			remaining := rest[i:]
			if strings.HasPrefix(remaining, prefix) || (level > 1 && strings.HasPrefix(remaining, strings.Repeat("#", level-1)+" ")) {
				return rest[:i]
			}
		}
	}
	return rest
}
