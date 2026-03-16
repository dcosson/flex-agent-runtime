package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// EvalExpression evaluates a basic arithmetic expression with +, -, *, / and parentheses.
func EvalExpression(expr string) (float64, error) {
	p := &exprParser{input: expr}
	v, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipWS()
	if p.pos != len(p.input) {
		return 0, fmt.Errorf("unexpected token at position %d", p.pos)
	}
	return v, nil
}

type exprParser struct {
	input string
	pos   int
}

func (p *exprParser) parseExpr() (float64, error) {
	v, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipWS()
		if p.match('+') {
			rhs, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			v += rhs
			continue
		}
		if p.match('-') {
			rhs, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			v -= rhs
			continue
		}
		return v, nil
	}
}

func (p *exprParser) parseTerm() (float64, error) {
	v, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		p.skipWS()
		if p.match('*') {
			rhs, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			v *= rhs
			continue
		}
		if p.match('/') {
			rhs, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			if rhs == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			v /= rhs
			continue
		}
		return v, nil
	}
}

func (p *exprParser) parseFactor() (float64, error) {
	p.skipWS()
	if p.match('+') {
		return p.parseFactor()
	}
	if p.match('-') {
		v, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		return -v, nil
	}
	if p.match('(') {
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipWS()
		if !p.match(')') {
			return 0, fmt.Errorf("missing closing parenthesis at position %d", p.pos)
		}
		return v, nil
	}
	return p.parseNumber()
}

func (p *exprParser) parseNumber() (float64, error) {
	p.skipWS()
	start := p.pos
	dotSeen := false
	for p.pos < len(p.input) {
		ch := rune(p.input[p.pos])
		if ch == '.' {
			if dotSeen {
				break
			}
			dotSeen = true
			p.pos++
			continue
		}
		if !unicode.IsDigit(ch) {
			break
		}
		p.pos++
	}
	if start == p.pos {
		trimmed := strings.TrimSpace(p.input)
		if trimmed == "" {
			return 0, fmt.Errorf("empty expression")
		}
		return 0, fmt.Errorf("expected number at position %d", p.pos)
	}
	val, err := strconv.ParseFloat(p.input[start:p.pos], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q: %w", p.input[start:p.pos], err)
	}
	return val, nil
}

func (p *exprParser) skipWS() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func (p *exprParser) match(ch byte) bool {
	if p.pos < len(p.input) && p.input[p.pos] == ch {
		p.pos++
		return true
	}
	return false
}
