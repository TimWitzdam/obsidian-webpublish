package documents

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	htmlrenderer "github.com/yuin/goldmark/renderer/html"
	htmlnode "golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

const idCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const shortIDLength = 8

// ErrEmptyDocument indicates no content was provided for rendering.
var ErrEmptyDocument = errors.New("documents: markdown content is empty")

var idPattern = regexp.MustCompile(`^[A-Za-z0-9]{6,12}$`)
var htmlSanitizer = buildHTMLSanitizer()

// Store persists markdown documents as rendered HTML files.
type Store struct {
	markdownDir string
	htmlDir     string
	converter   goldmark.Markdown
	mu          sync.Mutex
}

type metadataEntry struct {
	Key   string
	Value string
}

// Document represents a rendered markdown file.
type Document struct {
	ID           string
	MarkdownPath string
	HTMLPath     string
	CreatedAt    time.Time
}

// NewStore prepares the directory structure needed for persistence.
func NewStore(baseDir string) (*Store, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("documents: base directory is required")
	}

	markdownDir := filepath.Join(baseDir, "markdown")
	htmlDir := filepath.Join(baseDir, "html")

	for _, dir := range []string{baseDir, markdownDir, htmlDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("documents: create dir %s: %w", dir, err)
		}
	}

	conv := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(htmlrenderer.WithHardWraps(), htmlrenderer.WithXHTML()),
	)

	return &Store{
		markdownDir: markdownDir,
		htmlDir:     htmlDir,
		converter:   conv,
	}, nil
}

// Save converts the markdown bytes to HTML, writes both markdown and HTML files,
// and returns the persisted document metadata. The optional titleOverride is used
// when provided (e.g., from the uploader) instead of deriving the title from
// metadata/headings.
func (s *Store) Save(markdown []byte, titleOverride string) (*Document, error) {
	trimmed := bytes.TrimSpace(markdown)
	if len(trimmed) == 0 {
		return nil, ErrEmptyDocument
	}

	rendered, err := s.renderHTML(trimmed, titleOverride)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var doc *Document
	for attempt := 0; attempt < 6; attempt++ {
		id, err := generateID(shortIDLength)
		if err != nil {
			return nil, err
		}
		htmlPath := filepath.Join(s.htmlDir, id+".html")
		if _, err := os.Stat(htmlPath); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("documents: stat html path: %w", err)
		}

		markdownPath := filepath.Join(s.markdownDir, id+".md")
		if err := os.WriteFile(markdownPath, trimmed, 0o644); err != nil {
			return nil, fmt.Errorf("documents: write markdown: %w", err)
		}
		if err := os.WriteFile(htmlPath, rendered, 0o644); err != nil {
			return nil, fmt.Errorf("documents: write html: %w", err)
		}
		doc = &Document{
			ID:           id,
			MarkdownPath: markdownPath,
			HTMLPath:     htmlPath,
			CreatedAt:    time.Now().UTC(),
		}
		break
	}

	if doc == nil {
		return nil, errors.New("documents: could not allocate unique id")
	}

	return doc, nil
}

// LoadHTML returns the rendered HTML for a stored document ID.
func (s *Store) LoadHTML(id string) ([]byte, error) {
	if !idPattern.MatchString(id) {
		return nil, fs.ErrNotExist
	}

	htmlPath := filepath.Join(s.htmlDir, id+".html")
	data, err := os.ReadFile(htmlPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("documents: read html: %w", err)
	}
	return data, nil
}

func (s *Store) renderHTML(markdown []byte, titleOverride string) ([]byte, error) {
	meta, body := splitFrontmatter(markdown)
	if len(body) == 0 {
		body = markdown
	}

	var rendered bytes.Buffer
	if err := s.converter.Convert(body, &rendered); err != nil {
		return nil, fmt.Errorf("documents: render markdown: %w", err)
	}
	htmlBody := rendered.String()
	sanitizedBody := htmlSanitizer.Sanitize(htmlBody)

	title := deriveTitle(meta, body, titleOverride)
	if title == "" {
		title = "Shared note"
	}
	description := summarizeHTML(sanitizedBody)
	if description == "" {
		description = "Shared note published via Obsidian WebPublish."
	}

	var page bytes.Buffer
	if err := documentTemplate.Execute(&page, struct {
		Title       string
		Description string
		Metadata    []metadataEntry
		Body        template.HTML
		Generated   string
	}{
		Title:       title,
		Description: description,
		Metadata:    meta,
		Body:        template.HTML(sanitizedBody),
		Generated:   time.Now().Format(time.RFC1123),
	}); err != nil {
		return nil, fmt.Errorf("documents: wrap html: %w", err)
	}
	return page.Bytes(), nil
}

func generateID(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("documents: invalid id length")
	}
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("documents: random id: %w", err)
	}
	for i := range b {
		b[i] = idCharset[int(b[i])%len(idCharset)]
	}
	return string(b), nil
}

var documentTemplate = template.Must(template.New("doc").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta http-equiv="X-UA-Compatible" content="IE=edge" />
<title>{{.Title}}</title>
<meta name="viewport" content="width=device-width, initial-scale=1" />
<meta name="description" content="{{.Description}}" />
<meta property="og:title" content="{{.Title}}" />
<meta property="og:description" content="{{.Description}}" />
<meta property="og:type" content="article" />
<meta name="twitter:card" content="summary" />
<style>
body { font-family: "Inter", system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 3rem auto; max-width: 760px; padding: 0 1.5rem; line-height: 1.6; color: #0f172a; background-color: #f8fafc; }
h1, h2, h3, h4, h5, h6 { color: #0f172a; margin-top: 2.5rem; }
code, pre { font-family: "JetBrains Mono", "SFMono-Regular", Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace; background-color: #e2e8f0; border-radius: 6px; padding: 0.1rem 0.35rem; }
pre { padding: 1rem; overflow-x: auto; }
a { color: #2563eb; }
img { max-width: 100%; height: auto; display: block; margin: 1.5rem auto; }
blockquote { border-left: 4px solid #cbd5f5; padding-left: 1rem; color: #475569; }
table { width: 100%; border-collapse: collapse; margin: 1rem 0; }
th, td { border: 1px solid #cbd5e1; padding: 0.5rem 0.75rem; text-align: left; }
ul, ol { padding-left: 1.5rem; }
.metadata { background-color: #ffffff; border: 1px solid #e2e8f0; border-radius: 12px; padding: 1.25rem; margin-bottom: 2rem; box-shadow: 0 1px 2px rgba(15, 23, 42, 0.08); }
.metadata h2 { margin-top: 0; font-size: 1.1rem; letter-spacing: 0.02em; color: #0f172a; }
.metadata-table { width: 100%; border-collapse: collapse; }
.metadata-table th { width: 32%; font-weight: 600; text-transform: capitalize; color: #0f172a; }
.metadata-table td { color: #1e293b; }
.metadata-table tr + tr th, .metadata-table tr + tr td { border-top: 1px solid #e2e8f0; }
footer { margin-top: 3rem; font-size: 0.875rem; color: #475569; text-align: center; }
</style>
</head>
<body>
<main>
<header>
<h1>{{.Title}}</h1>
</header>
{{if .Metadata}}
<section class="metadata">
<h2>Properties</h2>
<table class="metadata-table">
<tbody>
{{range .Metadata}}
<tr>
<th>{{.Key}}</th>
<td>{{.Value}}</td>
</tr>
{{end}}
</tbody>
</table>
</section>
{{end}}
{{.Body}}
</main>
<footer>
<p>Published via Obsidian WebPublish · Generated {{.Generated}}</p>
</footer>
</body>
</html>`))

var frontmatterPattern = regexp.MustCompile(`(?s)^(?:\xef\xbb\xbf)?---\s*\r?\n(.*?)\r?\n---\s*(?:\r?\n|$)`)
var headingPattern = regexp.MustCompile(`(?m)^\s*#\s+(.+)$`)

func splitFrontmatter(markdown []byte) ([]metadataEntry, []byte) {
	loc := frontmatterPattern.FindSubmatchIndex(markdown)
	if loc == nil {
		return nil, markdown
	}
	entries, err := parseFrontmatter(markdown[loc[2]:loc[3]])
	if err != nil || len(entries) == 0 {
		return nil, markdown
	}
	body := bytes.TrimLeft(markdown[loc[1]:], "\r\n")
	return entries, body
}

func parseFrontmatter(data []byte) ([]metadataEntry, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 {
		return nil, nil
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil, nil
	}
	entries := make([]metadataEntry, 0, len(mapping.Content)/2)
	for i := 0; i < len(mapping.Content); i += 2 {
		keyNode := mapping.Content[i]
		valueNode := mapping.Content[i+1]
		key := strings.TrimSpace(keyNode.Value)
		if key == "" {
			continue
		}
		entries = append(entries, metadataEntry{
			Key:   key,
			Value: formatMetadataValue(valueNode),
		})
	}
	return entries, nil
}

func formatMetadataValue(node *yaml.Node) string {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Value
	case yaml.SequenceNode:
		values := make([]string, 0, len(node.Content))
		for _, child := range node.Content {
			formatted := strings.TrimSpace(formatMetadataValue(child))
			if formatted != "" {
				values = append(values, formatted)
			}
		}
		return strings.Join(values, ", ")
	case yaml.MappingNode:
		pairs := make([]string, 0, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			val := formatMetadataValue(node.Content[i+1])
			if key == "" || val == "" {
				continue
			}
			pairs = append(pairs, fmt.Sprintf("%s: %s", key, val))
		}
		return strings.Join(pairs, ", ")
	default:
		return ""
	}
}

func deriveTitle(meta []metadataEntry, body []byte, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	if title := findMetadataValue(meta, "title"); title != "" {
		return title
	}
	if heading := firstHeading(body); heading != "" {
		return heading
	}
	return ""
}

func findMetadataValue(meta []metadataEntry, key string) string {
	for _, entry := range meta {
		if strings.EqualFold(entry.Key, key) {
			return strings.TrimSpace(entry.Value)
		}
	}
	return ""
}

func firstHeading(body []byte) string {
	match := headingPattern.FindSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(string(match[1]))
}

func summarizeHTML(content string) string {
	text := extractTextFromHTML(content)
	compacted := strings.Join(strings.Fields(text), " ")
	return truncateRunes(compacted, 200)
}

func extractTextFromHTML(content string) string {
	node, err := htmlnode.Parse(strings.NewReader(content))
	if err != nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*htmlnode.Node)
	walk = func(n *htmlnode.Node) {
		if n.Type == htmlnode.TextNode {
			builder.WriteString(n.Data)
			builder.WriteRune(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 || len(text) == 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

func buildHTMLSanitizer() *bluemonday.Policy {
	policy := bluemonday.UGCPolicy()
	policy.AllowDataURIImages()
	policy.AllowElements("table", "thead", "tbody", "tr", "th", "td")
	return policy
}
