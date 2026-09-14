package tools

import (
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

type feedItem struct {
	Title   string
	Link    string
	Date    string
	Summary string
}

type rssDocument struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Date        string `xml:"pubDate"`
			Description string `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atomDocument struct {
	Entries []struct {
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Updated   string `xml:"updated"`
		Published string `xml:"published"`
		Summary   string `xml:"summary"`
		Content   string `xml:"content"`
	} `xml:"entry"`
}

func extractFeed(data []byte) (string, int, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", 0, feedParseError(err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		var items []feedItem
		switch strings.ToLower(start.Name.Local) {
		case "rss":
			var document rssDocument
			if err := decoder.DecodeElement(&document, &start); err != nil {
				return "", 0, feedParseError(err)
			}
			for _, item := range document.Channel.Items {
				items = append(items, feedItem{Title: item.Title, Link: item.Link, Date: item.Date, Summary: item.Description})
			}
		case "feed":
			var document atomDocument
			if err := decoder.DecodeElement(&document, &start); err != nil {
				return "", 0, feedParseError(err)
			}
			for _, entry := range document.Entries {
				link := ""
				for _, candidate := range entry.Links {
					if link == "" || candidate.Rel == "alternate" {
						link = candidate.Href
					}
					if candidate.Rel == "alternate" {
						break
					}
				}
				date := entry.Updated
				if date == "" {
					date = entry.Published
				}
				summary := entry.Summary
				if summary == "" {
					summary = entry.Content
				}
				items = append(items, feedItem{Title: entry.Title, Link: link, Date: date, Summary: summary})
			}
		default:
			return "", 0, fmt.Errorf("parse feed: root element must be rss or feed, got %s", start.Name.Local)
		}
		return formatFeed(items)
	}
}

func feedParseError(err error) error {
	if syntax, ok := err.(*xml.SyntaxError); ok {
		return fmt.Errorf("parse feed at line %d: %s", syntax.Line, syntax.Msg)
	}
	return fmt.Errorf("parse feed: %w", err)
}

func formatFeed(items []feedItem) (string, int, error) {
	kept := make([]feedItem, 0, len(items))
	dropped := 0
	for _, item := range items {
		item.Title = normalizeExtractedText(item.Title)
		item.Link = strings.TrimSpace(item.Link)
		item.Date = normalizeExtractedText(item.Date)
		item.Summary = normalizeFeedSummary(item.Summary)
		if item.Title == "" && item.Link == "" && item.Summary == "" {
			dropped++
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 {
		return "", dropped, fmt.Errorf("parse feed: no readable items")
	}
	var output strings.Builder
	for index, item := range kept {
		if index > 0 {
			output.WriteString("\n\n")
		}
		fmt.Fprintf(&output, "# %s\n%s\n%s\n%s", item.Title, feedField("link", item.Link), feedField("date", item.Date), feedField("summary", item.Summary))
	}
	return output.String(), dropped, nil
}

func feedField(label, value string) string {
	if value == "" {
		return label + ":"
	}
	return label + ": " + value
}

func normalizeExtractedText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizeFeedSummary(value string) string {
	if !strings.Contains(value, "<") {
		return normalizeExtractedText(value)
	}
	text, err := extractHTML([]byte(value), nil)
	if err != nil {
		return normalizeExtractedText(value)
	}
	return tightenExtractedPunctuation(normalizeExtractedText(text))
}

func tightenExtractedPunctuation(value string) string {
	return strings.NewReplacer(" .", ".", " ,", ",", " :", ":", " ;", ";", " !", "!", " ?", "?").Replace(value)
}

func extractArticle(data []byte, base *url.URL) (string, int, bool, error) {
	document, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return "", 0, false, fmt.Errorf("parse HTML: %w", err)
	}
	root := bestArticleRoot(document)
	if root == nil {
		text, err := extractHTML(data, base)
		return text, countSkippedHTML(document), true, err
	}
	return renderArticleRoot(root, base), countSkippedHTML(root), false, nil
}

func bestArticleRoot(document *html.Node) *html.Node {
	var best *html.Node
	bestScore, bestLength := 0, 0
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			score := articleRootScore(node)
			if score > 0 {
				length := len(normalizeExtractedText(nodeText(node)))
				if score > bestScore || score == bestScore && length > bestLength {
					best, bestScore, bestLength = node, score, length
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return best
}

func articleRootScore(node *html.Node) int {
	if node.Data == "article" {
		return 4
	}
	if node.Data == "main" {
		return 3
	}
	attributes := ""
	for _, attribute := range node.Attr {
		if attribute.Key == "role" && strings.EqualFold(attribute.Val, "main") {
			return 3
		}
		if attribute.Key == "class" || attribute.Key == "id" {
			attributes += " " + strings.ToLower(attribute.Val)
		}
	}
	for _, token := range strings.FieldsFunc(attributes, func(r rune) bool { return r < 'a' || r > 'z' }) {
		if token == "article" || token == "content" || token == "post" || token == "docs" || token == "documentation" {
			return 2
		}
	}
	return 0
}

func nodeText(root *html.Node) string {
	var output strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			output.WriteString(node.Data)
			output.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return output.String()
}

func renderArticleRoot(root *html.Node, base *url.URL) string {
	var output strings.Builder
	var render func(*html.Node)
	render = func(node *html.Node) {
		if articleSkippedNode(node) {
			return
		}
		block := node.Type == html.ElementNode && blockHTMLNode(node.Data)
		if block {
			writeHTMLBreak(&output)
		}
		if node.Type == html.TextNode {
			value := normalizeExtractedText(node.Data)
			if value != "" {
				if output.Len() > 0 && !strings.HasSuffix(output.String(), "\n") && !strings.HasSuffix(output.String(), " ") {
					output.WriteByte(' ')
				}
				output.WriteString(value)
			}
		}
		before := output.Len()
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			render(child)
		}
		if node.Type == html.ElementNode && node.Data == "a" && output.Len() > before {
			for _, attribute := range node.Attr {
				if attribute.Key == "href" && strings.TrimSpace(attribute.Val) != "" {
					href := strings.TrimSpace(attribute.Val)
					if parsed, parseErr := url.Parse(href); parseErr == nil && base != nil {
						href = base.ResolveReference(parsed).String()
					}
					fmt.Fprintf(&output, " (%s)", href)
					break
				}
			}
		}
		if block {
			writeHTMLBreak(&output)
		}
	}
	render(root)
	lines := strings.Split(strings.ReplaceAll(output.String(), "\r", ""), "\n")
	clean := lines[:0]
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			clean = append(clean, line)
		}
	}
	return tightenExtractedPunctuation(strings.Join(clean, "\n"))
}

func countSkippedHTML(root *html.Node) int {
	count := 0
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if articleSkippedNode(node) {
			count++
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return count
}

func articleSkippedNode(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}
	if skippedHTMLNode(node.Data) || node.Data == "footer" || node.Data == "aside" {
		return true
	}
	for _, attribute := range node.Attr {
		if attribute.Key != "class" && attribute.Key != "id" && attribute.Key != "role" {
			continue
		}
		for _, token := range strings.FieldsFunc(strings.ToLower(attribute.Val), func(r rune) bool { return r < 'a' || r > 'z' }) {
			if token == "cookie" || token == "consent" || token == "banner" || token == "navigation" || token == "advertisement" {
				return true
			}
		}
	}
	return false
}
