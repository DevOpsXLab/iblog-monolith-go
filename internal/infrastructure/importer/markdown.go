package importer

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// skip are elements whose content is never part of the story.
var skip = map[string]bool{
	"script": true, "style": true, "noscript": true, "nav": true, "header": true, "footer": true,
	"aside": true, "form": true, "button": true, "iframe": true, "svg": true, "template": true,
}

// converter writes HTML as Markdown. Blocks are separated by blank lines;
// inline elements write into the current block.
type converter struct {
	base   *url.URL
	blocks []string
	line   strings.Builder
	// quote and list prefixes for nested blocks
	prefix string
}

var spaces = regexp.MustCompile(`\s+`)

func (c *converter) flush() {
	s := strings.TrimSpace(c.line.String())
	c.line.Reset()
	if s != "" {
		c.blocks = append(c.blocks, c.prefix+s)
	}
}

func (c *converter) result() string {
	c.flush()
	return strings.TrimSpace(strings.Join(c.blocks, "\n\n"))
}

func (c *converter) children(n *html.Node) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.node(ch)
	}
}

func (c *converter) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		c.line.WriteString(escape(spaces.ReplaceAllString(n.Data, " ")))
		return
	case html.ElementNode:
	default:
		c.children(n)
		return
	}
	if skip[n.Data] {
		return
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		c.flush()
		level, _ := strconv.Atoi(n.Data[1:])
		if t := c.inline(n); t != "" {
			c.blocks = append(c.blocks, c.prefix+strings.Repeat("#", level)+" "+t)
		}
	case "p", "div", "section", "figure", "figcaption", "article", "main":
		c.flush()
		c.children(n)
		c.flush()
	case "br":
		c.line.WriteString("  \n" + c.prefix)
	case "hr":
		c.flush()
		c.blocks = append(c.blocks, "---")
	case "strong", "b":
		c.wrap(n, "**")
	case "em", "i":
		c.wrap(n, "*")
	case "code":
		if t := text(n); t != "" {
			c.line.WriteString("`" + strings.ReplaceAll(t, "`", "") + "`")
		}
	case "pre":
		c.flush()
		code := rawText(n)
		lang := strings.TrimPrefix(attr(firstElem(n, "code"), "class"), "language-")
		if strings.ContainsAny(lang, " \t") {
			lang = ""
		}
		c.blocks = append(c.blocks, "```"+lang+"\n"+strings.Trim(code, "\n")+"\n```")
	case "a":
		t := c.inline(n)
		if href := resolve(c.base, attr(n, "href")); href != "" && t != "" {
			c.line.WriteString("[" + t + "](" + href + ")")
		} else {
			c.line.WriteString(t)
		}
	case "img":
		if src := resolve(c.base, first(attr(n, "src"), attr(n, "data-src"))); src != "" {
			c.flush()
			c.blocks = append(c.blocks, c.prefix+"!["+escape(attr(n, "alt"))+"]("+src+")")
		}
	case "blockquote":
		c.flush()
		old := c.prefix
		c.prefix += "> "
		c.children(n)
		c.flush()
		c.prefix = old
	case "ul", "ol":
		c.flush()
		c.list(n, n.Data == "ol")
	default:
		c.children(n)
	}
}

func (c *converter) wrap(n *html.Node, mark string) {
	if t := c.inline(n); t != "" {
		c.line.WriteString(mark + t + mark)
	}
}

// inline renders n's content as one line of Markdown.
func (c *converter) inline(n *html.Node) string {
	sub := converter{base: c.base}
	sub.children(n)
	return strings.TrimSpace(strings.ReplaceAll(sub.result(), "\n\n", " "))
}

func (c *converter) list(n *html.Node, ordered bool) {
	var items []string
	for li := n.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.Data != "li" {
			continue
		}
		mark := "- "
		if ordered {
			mark = strconv.Itoa(len(items)+1) + ". "
		}
		sub := converter{base: c.base}
		sub.children(li)
		body := strings.ReplaceAll(sub.result(), "\n\n", "\n")
		if body != "" {
			items = append(items, c.prefix+mark+strings.ReplaceAll(body, "\n", "\n"+c.prefix+"   "))
		}
	}
	if len(items) > 0 {
		c.blocks = append(c.blocks, strings.Join(items, "\n"))
	}
}

func firstElem(n *html.Node, tag string) *html.Node {
	if f := find(n, tag); f != nil {
		return f
	}
	return n
}

func rawText(n *html.Node) string {
	var b strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.Data == "br" {
			b.WriteByte('\n')
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			f(ch)
		}
	}
	f(n)
	return b.String()
}

var mdSpecial = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "<", "&lt;")

func escape(s string) string { return mdSpecial.Replace(s) }
