package lint

// slopPhrases are filler words and phrases that add no information (REQ-063). A profile adds
// more with lint.slop_extra. Written for Speccy; no third-party list is imported.
var slopPhrases = []string{
	"delve", "delves", "delving",
	"robust", "seamless", "seamlessly",
	"leverage", "leverages", "leveraging",
	"utilize", "utilizes", "utilizing", "utilise", "utilises", "utilising",
	"cutting-edge", "state-of-the-art", "best-in-class", "world-class", "next-generation",
	"game-changer", "game-changing", "revolutionize", "revolutionise",
	"holistic", "paradigm", "synergies",
	"it's worth noting", "it is worth noting", "it's important to note", "it is important to note",
	"in today's fast-paced", "in the realm of", "a testament to",
	"plays a crucial role", "plays a pivotal role", "plays a vital role",
	"navigate the complexities", "unlock the power", "harness the power", "unleash",
	"tapestry", "embark on", "elevate",
	"empower", "empowers", "empowering",
	"at the end of the day", "moving forward", "going forward",
	"in order to", "each and every", "first and foremost",
	"comprehensive solution", "end-to-end solution", "streamlined",
}

// weaselWords are vague quantities and hedges (Appendix B, lint.weasel).
var weaselWords = []string{
	"some", "various", "several", "many", "a few", "a lot of", "numerous",
	"as needed", "as appropriate", "if necessary", "where applicable", "when possible",
	"should probably", "might", "could possibly", "perhaps", "arguably",
	"etc.", "and so on", "and so forth",
	"fairly", "quite", "relatively", "reasonably", "somewhat",
}

// commonAcronyms need no definition (lint.undefined-acronym).
var commonAcronyms = map[string]bool{}

func init() {
	for _, a := range []string{
		"API", "APIs", "HTTP", "HTTPS", "URL", "URI", "JSON", "YAML", "XML", "HTML", "CSS", "SQL", "CSV", "PDF",
		"UI", "UX", "ID", "IDs", "UUID", "CLI", "SDK", "OK", "UTC", "TLS", "SSL", "DNS", "IP", "TCP", "UDP",
		"CPU", "GPU", "RAM", "OS", "AWS", "GCP", "CI", "CD", "PR", "MVP", "FAQ", "US", "UK", "EU", "ASCII",
		"UTF", "REST", "SSO", "JWT", "SMS", "KB", "MB", "GB", "TB", "AM", "PM", "AI", "LLM", "LLMs", "PRD",
		"SDD", "README", "SSH", "IDE", "QA", "SLA", "SLO", "ETA", "GUI", "VPN", "CDN", "DB",
		"MUST", "SHOULD", "MAY", "NOT", "COULD", "SHALL", "REQUIRED", "RECOMMENDED", "OPTIONAL",
		"NOTE", "TBD", "TODO", "FIXME", "XXX", "AND", "OR", "IF", "THEN", "ELSE", "NULL", "GET", "POST",
		"PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "ISO", "RFC", "OAuth", "OIDC", "SAML", "WCAG", "AA", "AAA",
	} {
		commonAcronyms[a] = true
	}
}

// htmlTags are HTML element names. "<table>" is markup; "<Product name>" is a placeholder.
var htmlTags = map[string]bool{}

func init() {
	for _, t := range []string{
		"a", "abbr", "address", "area", "article", "aside", "audio", "b", "base", "bdi", "bdo", "blockquote",
		"body", "br", "button", "canvas", "caption", "cite", "code", "col", "colgroup", "data", "datalist",
		"dd", "del", "details", "dfn", "dialog", "div", "dl", "dt", "em", "embed", "fieldset", "figcaption",
		"figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "head", "header", "hr", "html", "i",
		"iframe", "img", "input", "ins", "kbd", "label", "legend", "li", "link", "main", "map", "mark", "meta",
		"meter", "nav", "noscript", "object", "ol", "optgroup", "option", "output", "p", "param", "picture",
		"pre", "progress", "q", "rp", "rt", "ruby", "s", "samp", "script", "section", "select", "small",
		"source", "span", "strong", "style", "sub", "summary", "sup", "svg", "table", "tbody", "td",
		"template", "textarea", "tfoot", "th", "thead", "time", "title", "tr", "track", "u", "ul", "var",
		"video", "wbr", "center", "font", "path", "g", "circle", "rect", "line",
	} {
		htmlTags[t] = true
	}
}

// irregularParticiples are past participles that do not end in -ed (lint.passive-voice).
const irregularParticiples = `known|made|done|given|taken|written|seen|built|sent|held|kept|set|shown|found|put|` +
	`run|read|chosen|drawn|begun|broken|thrown|told|paid|left|lost|met|sold|stolen|understood|won|` +
	`bound|brought|bought|caught|fed|felt|fought|forgotten|frozen|hidden|hit|hurt|led|lent|lit|meant|` +
	`said|shut|spent|spoken|split|spread|struck|taught|thought|torn|worn|driven|eaten|fallen|flown|` +
	`grown|laid|ridden|risen|shaken|sung|sworn|woken|withdrawn|overridden|rewritten|undone`
