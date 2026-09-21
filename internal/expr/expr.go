// Package expr evaluates GitLab CI/CD variable expressions used in rules:if.
package expr

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Eval returns whether a GitLab rules:if expression is true.
func Eval(src string, vars map[string]string) (bool, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return false, nil
	}
	p := &parser{tokens: tokenize(src), vars: vars}
	v, err := p.parseOr()
	if err != nil {
		return false, err
	}
	if p.pos < len(p.tokens) && p.tokens[p.pos].k != tEOF {
		return false, fmt.Errorf("unexpected token %q", p.tokens[p.pos].val)
	}
	return v.Truthy(), nil
}

type kind int

const (
	tEOF kind = iota
	tOr
	tAnd
	tEq
	tNE
	tMatch
	tNotMatch
	tLParen
	tRParen
	tNot
	tVar
	tStr
	tPat
	tNull
)

type token struct {
	k   kind
	val string
}

func tokenize(s string) []token {
	var out []token
	i := 0
	for i < len(s) {
		for i < len(s) && unicode.IsSpace(rune(s[i])) {
			i++
		}
		if i >= len(s) {
			break
		}
		switch {
		case strings.HasPrefix(s[i:], "||"):
			out = append(out, token{k: tOr, val: "||"})
			i += 2
		case strings.HasPrefix(s[i:], "&&"):
			out = append(out, token{k: tAnd, val: "&&"})
			i += 2
		case strings.HasPrefix(s[i:], "=="):
			out = append(out, token{k: tEq, val: "=="})
			i += 2
		case strings.HasPrefix(s[i:], "!="):
			out = append(out, token{k: tNE, val: "!="})
			i += 2
		case strings.HasPrefix(s[i:], "=~"):
			out = append(out, token{k: tMatch, val: "=~"})
			i += 2
		case strings.HasPrefix(s[i:], "!~"):
			out = append(out, token{k: tNotMatch, val: "!~"})
			i += 2
		case s[i] == '(':
			out = append(out, token{k: tLParen, val: "("})
			i++
		case s[i] == ')':
			out = append(out, token{k: tRParen, val: ")"})
			i++
		case s[i] == '!' && (i+1 >= len(s) || (s[i+1] != '=' && s[i+1] != '~')):
			out = append(out, token{k: tNot, val: "!"})
			i++
		case s[i] == '"' || s[i] == '\'':
			q := s[i]
			j := i + 1
			var b strings.Builder
			for j < len(s) && s[j] != q {
				if s[j] == '\\' && j+1 < len(s) {
					b.WriteByte(s[j+1])
					j += 2
					continue
				}
				b.WriteByte(s[j])
				j++
			}
			out = append(out, token{k: tStr, val: b.String()})
			if j < len(s) {
				j++
			}
			i = j
		case s[i] == '/':
			j := i + 1
			var b strings.Builder
			for j < len(s) && s[j] != '/' {
				if s[j] == '\\' && j+1 < len(s) {
					b.WriteByte(s[j])
					b.WriteByte(s[j+1])
					j += 2
					continue
				}
				b.WriteByte(s[j])
				j++
			}
			if j < len(s) {
				j++
			}
			out = append(out, token{k: tPat, val: b.String()})
			i = j
		case s[i] == '$':
			j := i + 1
			if j < len(s) && s[j] == '{' {
				j++
				k := j
				for k < len(s) && s[k] != '}' {
					k++
				}
				out = append(out, token{k: tVar, val: s[j:k]})
				if k < len(s) {
					k++
				}
				i = k
				continue
			}
			k := j
			for k < len(s) && (unicode.IsLetter(rune(s[k])) || unicode.IsDigit(rune(s[k])) || s[k] == '_') {
				k++
			}
			out = append(out, token{k: tVar, val: s[j:k]})
			i = k
		default:
			k := i
			for k < len(s) && !unicode.IsSpace(rune(s[k])) && !strings.ContainsRune("()|&!=~", rune(s[k])) {
				k++
			}
			word := s[i:k]
			if strings.EqualFold(word, "null") {
				out = append(out, token{k: tNull, val: word})
			} else {
				out = append(out, token{k: tStr, val: word})
			}
			i = k
		}
	}
	out = append(out, token{k: tEOF})
	return out
}

type value struct {
	str   string
	null  bool
	regex bool
}

func (v value) Truthy() bool {
	if v.null {
		return false
	}
	return v.str != ""
}

type parser struct {
	tokens []token
	pos    int
	vars   map[string]string
}

func (p *parser) peek() token {
	if p.pos >= len(p.tokens) {
		return token{k: tEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) eat(k kind) bool {
	if p.peek().k == k {
		p.pos++
		return true
	}
	return false
}

func (p *parser) parseOr() (value, error) {
	left, err := p.parseAnd()
	if err != nil {
		return value{}, err
	}
	for p.eat(tOr) {
		right, err := p.parseAnd()
		if err != nil {
			return value{}, err
		}
		left = value{str: boolStr(left.Truthy() || right.Truthy())}
	}
	return left, nil
}

func (p *parser) parseAnd() (value, error) {
	left, err := p.parseCmp()
	if err != nil {
		return value{}, err
	}
	for p.eat(tAnd) {
		right, err := p.parseCmp()
		if err != nil {
			return value{}, err
		}
		left = value{str: boolStr(left.Truthy() && right.Truthy())}
	}
	return left, nil
}

func (p *parser) parseCmp() (value, error) {
	if p.eat(tNot) {
		v, err := p.parseCmp()
		if err != nil {
			return value{}, err
		}
		return value{str: boolStr(!v.Truthy())}, nil
	}
	left, err := p.parsePrimary()
	if err != nil {
		return value{}, err
	}
	switch {
	case p.eat(tEq):
		right, err := p.parsePrimary()
		if err != nil {
			return value{}, err
		}
		return value{str: boolStr(left.str == right.str && left.null == right.null)}, nil
	case p.eat(tNE):
		right, err := p.parsePrimary()
		if err != nil {
			return value{}, err
		}
		return value{str: boolStr(left.str != right.str || left.null != right.null)}, nil
	case p.eat(tMatch):
		right, err := p.parsePrimary()
		if err != nil {
			return value{}, err
		}
		ok, err := match(left.str, right)
		if err != nil {
			return value{}, err
		}
		return value{str: boolStr(ok)}, nil
	case p.eat(tNotMatch):
		right, err := p.parsePrimary()
		if err != nil {
			return value{}, err
		}
		ok, err := match(left.str, right)
		if err != nil {
			return value{}, err
		}
		return value{str: boolStr(!ok)}, nil
	}
	return left, nil
}

func (p *parser) parsePrimary() (value, error) {
	t := p.peek()
	switch t.k {
	case tLParen:
		p.pos++
		v, err := p.parseOr()
		if err != nil {
			return value{}, err
		}
		if !p.eat(tRParen) {
			return value{}, fmt.Errorf("missing )")
		}
		return v, nil
	case tVar:
		p.pos++
		val, ok := p.vars[t.val]
		if !ok || val == "" {
			return value{null: true}, nil
		}
		return value{str: val}, nil
	case tStr:
		p.pos++
		return value{str: expandVars(t.val, p.vars)}, nil
	case tPat:
		p.pos++
		return value{str: t.val, regex: true}, nil
	case tNull:
		p.pos++
		return value{null: true}, nil
	default:
		return value{}, fmt.Errorf("unexpected token %q", t.val)
	}
}

func match(left string, right value) (bool, error) {
	if left == "" {
		return false, nil
	}
	pat := right.str
	re, err := regexp.Compile(pat)
	if err != nil {
		return false, fmt.Errorf("invalid regex %q: %w", pat, err)
	}
	return re.MatchString(left), nil
}

func expandVars(s string, vars map[string]string) string {
	return regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}|\$([A-Za-z0-9_]+)`).ReplaceAllStringFunc(s, func(m string) string {
		name := strings.TrimPrefix(m, "$")
		name = strings.TrimPrefix(name, "{")
		name = strings.TrimSuffix(name, "}")
		return vars[name]
	})
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return ""
}
