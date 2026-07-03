// Package instructions implements discovery, loading, and merging of
// project-level instruction files (CLAUDE.md, AGENT.md, .github/copilot-instructions.md, etc.)
// with a layered priority system compatible with Claude Code, OpenAI Codex, and GitHub Copilot.
package instructions

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// Known instruction file names, checked in order.
// Includes GitHub Copilot's convention: .github/copilot-instructions.md
var KnownFileNames = []string{
	"CLAUDE.md",
	"AGENT.md",
	".github/copilot-instructions.md",
}

// LayerScope defines the scope of an instruction layer.
type LayerScope string

const (
	ScopeManaged   LayerScope = "managed"   // /etc/taiyiflow/instructions/ or equivalent
	ScopeUser      LayerScope = "user"      // ~/.snz1dp/, ~/.claude/, ~/.codex/
	ScopeProject   LayerScope = "project"   // ./CLAUDE.md or ./AGENT.md
	ScopeLocal     LayerScope = "local"     // ./CLAUDE.local.md or ./AGENT.local.md
	ScopeSubdir    LayerScope = "subdir"    // subdirectory-level (walk up from cwd)
	ScopeWorkspace LayerScope = "workspace" // workspace root (reserved)
)

// maxFileSize is the maximum allowed size for a single instruction file (256KB).
const maxFileSize = 256 * 1024

// maxTotalSize is the maximum combined size for all instruction layers (1MB).
const maxTotalSize = 1024 * 1024

// Layer represents a single instruction layer with its metadata.
type Layer struct {
	Scope    LayerScope
	FilePath string
	Content  string
	Priority int // lower number = higher priority
	Position int // original discovery order for stable sort
}

// MergedResult holds the final merged instructions from all layers.
type MergedResult struct {
	Content string
	Sources []string
	Layers  []Layer
}

// InstructionLayer is the JSON-serializable representation of a single layer,
// used when transmitting instructions to the backend via StreamRequest.
type InstructionLayer struct {
	Scope    string `json:"scope"`
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
	Priority int    `json:"priority"`
}

// InstructionPackage is the structured payload sent to the backend.
// It contains both the individual layers and the merged text.
type InstructionPackage struct {
	Layers []InstructionLayer `json:"layers"`
	Merged string             `json:"merged"`
}

// Load discovers and loads all instruction layers for the given workspace,
// returning a merged result with layers ordered by priority.
func Load(workspaceDir string) (*MergedResult, error) {
	return LoadWithCwd(workspaceDir, "")
}

// LoadWithCwd is like Load but accepts an explicit cwd for subdirectory
// discovery. When cwd is empty, os.Getwd() is used. This is primarily
// useful for testing subdirectory traversal in a controlled environment.
func LoadWithCwd(workspaceDir, cwd string) (*MergedResult, error) {
	layers, err := discoverLayersWithCwd(workspaceDir, cwd)
	if err != nil {
		return nil, fmt.Errorf("discovering instruction layers: %w", err)
	}

	return mergeLayers(layers), nil
}

// ToPackage converts a MergedResult into a JSON-serializable InstructionPackage
// suitable for inclusion in StreamRequest.ClientInstructions.
// Returns nil if there are no layers.
func (r *MergedResult) ToPackage() *InstructionPackage {
	if r == nil || len(r.Layers) == 0 {
		return nil
	}

	serializable := make([]InstructionLayer, 0, len(r.Layers))
	totalSize := 0
	for _, l := range r.Layers {
		trimmed := strings.TrimSpace(l.Content)
		if trimmed == "" {
			continue
		}
		if totalSize+len(trimmed) > maxTotalSize {
			break // enforce total size cap
		}
		serializable = append(serializable, InstructionLayer{
			Scope:    string(l.Scope),
			FilePath: l.FilePath,
			Content:  trimmed,
			Priority: l.Priority,
		})
		totalSize += len(trimmed)
	}

	if len(serializable) == 0 {
		return nil
	}

	// Regenerate merged text to match the (possibly truncated) layers,
	// ensuring Layers and Merged are always consistent.
	merged := buildMergedFromSerializable(serializable)

	return &InstructionPackage{
		Layers: serializable,
		Merged: merged,
	}
}

// buildMergedFromSerializable reconstructs the merged text from serialized layers,
// using the same header format as mergeLayers.
func buildMergedFromSerializable(layers []InstructionLayer) string {
	parts := make([]string, 0, len(layers))
	for _, l := range layers {
		header := fmt.Sprintf("[Instructions from %s (%s, priority %d)]",
			l.FilePath, l.Scope, l.Priority)
		parts = append(parts, fmt.Sprintf("%s\n%s", header, l.Content))
	}
	return strings.Join(parts, "\n\n")
}

// discoverLayers finds all instruction files across all scopes using os.Getwd().
func discoverLayers(workspaceDir string) ([]Layer, error) {
	return discoverLayersWithCwd(workspaceDir, "")
}

// discoverLayersWithCwd finds all instruction files across all scopes.
// When cwd is empty, os.Getwd() is used.
func discoverLayersWithCwd(workspaceDir, cwd string) ([]Layer, error) {
	var layers []Layer
	pos := 0

	// 1. Managed layer (system-wide, e.g. /etc/taiyiflow/instructions/)
	managedDir := resolveManagedDir()
	if managedDir != "" {
		for _, name := range KnownFileNames {
			fp := filepath.Join(managedDir, name)
			if content, ok := readFileIfExists(fp); ok {
				layers = append(layers, Layer{
					Scope: ScopeManaged, FilePath: fp, Content: content,
					Priority: 0, Position: pos,
				})
				pos++
			}
		}
	}

	// 2. User layers — check multiple tool-specific directories
	//    Priority: ~/.snz1dp/ (10) > ~/.claude/ (11) > ~/.codex/ (12)
	//    This allows taiyiflow-native config to take precedence while
	//    still picking up Claude Code and Codex instruction files.
	userDirs := resolveUserDirs()
	for _, ud := range userDirs {
		if ud.dir == "" {
			continue
		}
		for _, name := range KnownFileNames {
			fp := filepath.Join(ud.dir, name)
			if content, ok := readFileIfExists(fp); ok {
				layers = append(layers, Layer{
					Scope: ScopeUser, FilePath: fp, Content: content,
					Priority: ud.priority, Position: pos,
				})
				pos++
			}
		}
	}

	// 3. Project layer (workspace root)
	if workspaceDir != "" {
		for _, name := range KnownFileNames {
			fp := filepath.Join(workspaceDir, name)
			if content, ok := readFileIfExists(fp); ok {
				layers = append(layers, Layer{
					Scope: ScopeProject, FilePath: fp, Content: content,
					Priority: 20, Position: pos,
				})
				pos++
			}
		}

		// 4. Local layer (workspace root, .local.md variants)
		for _, name := range KnownFileNames {
			localName := strings.TrimSuffix(name, ".md") + ".local.md"
			fp := filepath.Join(workspaceDir, localName)
			if content, ok := readFileIfExists(fp); ok {
				layers = append(layers, Layer{
					Scope: ScopeLocal, FilePath: fp, Content: content,
					Priority: 25, Position: pos,
				})
				pos++
			}
		}

		// 5. Subdirectory layers (walk up from cwd to workspace root)
		if cwd == "" {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				cwd = ""
			}
		}
		if cwd != "" {
			subdirLayers, newPos := discoverSubdirLayers(workspaceDir, cwd, pos)
			layers = append(layers, subdirLayers...)
			pos = newPos
		}
	}

	return layers, nil
}

// mergeLayers sorts layers by priority (stable) and merges their content
// into a single MergedResult. Each layer's content is wrapped in a section
// header that identifies its source file and scope.
func mergeLayers(layers []Layer) *MergedResult {
	if len(layers) == 0 {
		return &MergedResult{}
	}

	// Stable sort: by Priority ascending, then by Position ascending.
	sort.SliceStable(layers, func(i, j int) bool {
		if layers[i].Priority != layers[j].Priority {
			return layers[i].Priority < layers[j].Priority
		}
		return layers[i].Position < layers[j].Position
	})

	var (
		parts   []string
		sources []string
	)

	for _, l := range layers {
		trimmed := strings.TrimSpace(l.Content)
		if trimmed == "" {
			continue
		}
		header := fmt.Sprintf("[Instructions from %s (%s, priority %d)]",
			l.FilePath, l.Scope, l.Priority)
		section := fmt.Sprintf("%s\n%s", header, trimmed)
		parts = append(parts, section)
		sources = append(sources, l.FilePath)
	}

	return &MergedResult{
		Content: strings.Join(parts, "\n\n"),
		Sources: sources,
		Layers:  layers,
	}
}

// readFileIfExists reads a file if it exists and is within the size limit.
// Returns the content and true if the file was read, or empty string and false otherwise.
func readFileIfExists(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return "", false
	}
	if info.Size() > maxFileSize {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// isSubPath reports whether child is strictly under parent in the filesystem tree.
// It uses filepath.Rel to avoid prefix-matching pitfalls (e.g. /workspace vs /workspace-other).
func isSubPath(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != "."
}

// discoverSubdirLayers walks up from cwd to workspaceDir, collecting instruction files.
// Returns the discovered layers and the updated position counter.
func discoverSubdirLayers(workspaceDir, cwd string, startPos int) ([]Layer, int) {
	if workspaceDir == "" || cwd == "" || cwd == workspaceDir || !isSubPath(cwd, workspaceDir) {
		return nil, startPos
	}
	var layers []Layer
	pos := startPos
	dir := cwd
	subPriority := 30
	for dir != workspaceDir && isSubPath(dir, workspaceDir) {
		for _, name := range KnownFileNames {
			fp := filepath.Join(dir, name)
			if content, ok := readFileIfExists(fp); ok {
				layers = append(layers, Layer{
					Scope: ScopeSubdir, FilePath: fp, Content: content,
					Priority: subPriority, Position: pos,
				})
				pos++
				subPriority++
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return layers, pos
}

// userDirEntry pairs a directory path with its priority for user-scope discovery.
type userDirEntry struct {
	dir      string
	priority int
}

// resolveManagedDir returns the system-wide managed instruction directory.
func resolveManagedDir() string {
	if v := os.Getenv("TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR"); v != "" {
		return v
	}
	if os.PathSeparator == '\\' {
		return "" // Windows: no default managed dir
	}
	return "/etc/snz1dp/instructions"
}

// resolveUserDirs returns all user-level instruction directories in priority order.
// Supports snz1dp (SNZ1DP_HOME or ~/.snz1dp/), Claude Code (~/.claude/), and Codex (~/.codex/).
func resolveUserDirs() []userDirEntry {
	// Environment variable override: use it as the sole user dir with highest priority.
	if v := os.Getenv("TAIYIFLOW_USER_INSTRUCTIONS_DIR"); v != "" {
		return []userDirEntry{{dir: v, priority: 10}}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	// snz1dp 目录优先从 SNZ1DP_HOME 环境变量提取，未设置时回退到 ~/.snz1dp。
	snz1dpDir, err := internalconfig.DefaultRootDir()
	if err != nil {
		snz1dpDir = filepath.Join(home, ".snz1dp")
	}

	return []userDirEntry{
		{dir: snz1dpDir, priority: 10},
		{dir: filepath.Join(home, ".claude"), priority: 11},
		{dir: filepath.Join(home, ".codex"), priority: 12},
	}
}

// resolveUserDir returns the primary user-level instruction directory.
// Kept for backward compatibility; new code should use resolveUserDirs.
func resolveUserDir() string {
	if v := os.Getenv("TAIYIFLOW_USER_INSTRUCTIONS_DIR"); v != "" {
		return v
	}
	// 优先从 SNZ1DP_HOME 环境变量提取，未设置时回退到 ~/.snz1dp。
	dir, err := internalconfig.DefaultRootDir()
	if err != nil {
		return ""
	}
	return dir
}

// FormatSummary returns a human-readable summary of the merged result,
// listing all discovered sources and their scopes.
func (r *MergedResult) FormatSummary() string {
	if len(r.Layers) == 0 {
		return "No instruction files found."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Loaded %d instruction layer(s):\n", len(r.Layers)))
	for i, l := range r.Layers {
		b.WriteString(fmt.Sprintf("  %d. [%s] %s (priority %d)\n",
			i+1, l.Scope, l.FilePath, l.Priority))
	}
	return b.String()
}
