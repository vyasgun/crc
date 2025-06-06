package godot

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	errEmptyInput      = errors.New("empty input")
	errUnsuitableInput = errors.New("unsuitable input")
)

// specialReplacer is a replacer for some types of special lines in comments,
// which shouldn't be checked. For example, if a comment ends with a block of
// code it should not necessarily have a period at the end.
const specialReplacer = "<godotSpecialReplacer>"

type parsedFile struct {
	fset  *token.FileSet
	file  *ast.File
	lines []string
}

func newParsedFile(file *ast.File, fset *token.FileSet) (*parsedFile, error) {
	if file == nil || fset == nil || len(file.Comments) == 0 {
		return nil, errEmptyInput
	}

	pf := parsedFile{
		fset: fset,
		file: file,
	}

	// Read original file. This is necessary for making a replacements for
	// inline comments. I couldn't find a better way to get original line
	// with code and comment without reading the file. Function `Format`
	// from "go/format" won't help here if the original file is not gofmt-ed.

	filename := getFilename(fset, file)

	if !strings.HasSuffix(filename, ".go") {
		return nil, errEmptyInput
	}

	var err error

	pf.lines, err = readFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return &pf, nil
}

// getComments extracts comments from a file.
func (pf *parsedFile) getComments(scope Scope, exclude []*regexp.Regexp) []comment {
	var comments []comment
	decl := pf.getDeclarationComments(exclude)
	switch scope {
	case AllScope:
		// All comments
		comments = pf.getAllComments(exclude)
	case NoInlineScope:
		// All except inline comments
		comments = pf.getNoInlineComments(exclude)
	case TopLevelScope:
		// All top level comments and comments from the inside
		// of top level blocks
		comments = append(
			pf.getBlockComments(exclude),
			pf.getTopLevelComments(exclude)...,
		)
	case DeclScope:
		// Top level declaration comments and comments from the inside
		// of top level blocks
		comments = append(pf.getBlockComments(exclude), decl...)
	}

	// Set `decl` flag
	setDecl(comments, decl)

	return comments
}

// getBlockComments gets comments from the inside of top level blocks:
// var (...), const (...).
func (pf *parsedFile) getBlockComments(exclude []*regexp.Regexp) []comment {
	var comments []comment
	for _, decl := range pf.file.Decls {
		d, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		// No parenthesis == no block
		if d.Lparen == 0 {
			continue
		}
		for _, c := range pf.file.Comments {
			if c == nil || len(c.List) == 0 {
				continue
			}
			// Skip comments outside this block
			if d.Lparen > c.Pos() || c.Pos() > d.Rparen {
				continue
			}
			// Skip comments that are not top-level for this block
			// (the block itself is top level, so comments inside this block
			// would be on column 2)
			//nolint:gomnd
			if pf.fset.Position(c.Pos()).Column != 2 {
				continue
			}
			firstLine := pf.fset.Position(c.Pos()).Line
			lastLine := pf.fset.Position(c.End()).Line
			comments = append(comments, comment{
				lines: pf.lines[firstLine-1 : lastLine],
				text:  getText(c, exclude),
				start: pf.fset.Position(c.List[0].Slash),
			})
		}
	}
	return comments
}

// getTopLevelComments gets all top level comments.
func (pf *parsedFile) getTopLevelComments(exclude []*regexp.Regexp) []comment {
	var comments []comment //nolint:prealloc
	for _, c := range pf.file.Comments {
		if c == nil || len(c.List) == 0 {
			continue
		}
		if pf.fset.Position(c.Pos()).Column != 1 {
			continue
		}
		firstLine := pf.fset.Position(c.Pos()).Line
		lastLine := pf.fset.Position(c.End()).Line
		comments = append(comments, comment{
			lines: pf.lines[firstLine-1 : lastLine],
			text:  getText(c, exclude),
			start: pf.fset.Position(c.List[0].Slash),
		})
	}
	return comments
}

// getDeclarationComments gets top level declaration comments.
func (pf *parsedFile) getDeclarationComments(exclude []*regexp.Regexp) []comment {
	var comments []comment //nolint:prealloc
	for _, decl := range pf.file.Decls {
		var cg *ast.CommentGroup
		switch d := decl.(type) {
		case *ast.GenDecl:
			cg = d.Doc
		case *ast.FuncDecl:
			cg = d.Doc
		}

		if cg == nil || len(cg.List) == 0 {
			continue
		}

		firstLine := pf.fset.Position(cg.Pos()).Line
		lastLine := pf.fset.Position(cg.End()).Line
		comments = append(comments, comment{
			lines: pf.lines[firstLine-1 : lastLine],
			text:  getText(cg, exclude),
			start: pf.fset.Position(cg.List[0].Slash),
		})
	}
	return comments
}

// getNoInlineComments gets all except inline comments.
func (pf *parsedFile) getNoInlineComments(exclude []*regexp.Regexp) []comment {
	var comments []comment //nolint:prealloc
	for _, c := range pf.file.Comments {
		if c == nil || len(c.List) == 0 {
			continue
		}
		firstLine := pf.fset.Position(c.Pos()).Line
		lastLine := pf.fset.Position(c.End()).Line

		c := comment{
			lines: pf.lines[firstLine-1 : lastLine],
			start: pf.fset.Position(c.List[0].Slash),
			text:  getText(c, exclude),
		}

		// Skip inline
		if len(c.lines) == 1 {
			before := c.lines[0][:c.start.Column-1]
			if len(strings.TrimSpace(before)) > 0 {
				continue
			}
		}
		comments = append(comments, c)
	}
	return comments
}

// getAllComments gets every single comment from the file.
func (pf *parsedFile) getAllComments(exclude []*regexp.Regexp) []comment {
	var comments []comment //nolint:prealloc
	for _, c := range pf.file.Comments {
		if c == nil || len(c.List) == 0 {
			continue
		}
		firstLine := pf.fset.Position(c.Pos()).Line
		lastLine := pf.fset.Position(c.End()).Line
		comments = append(comments, comment{
			lines: pf.lines[firstLine-1 : lastLine],
			start: pf.fset.Position(c.List[0].Slash),
			text:  getText(c, exclude),
		})
	}
	return comments
}

// getText extracts text from comment. If the comment is a special block
// (e.g., CGO code), a block of empty lines is returned. If comment contains
// special lines (e.g., tags or indented code examples), they are replaced
// with `specialReplacer` to skip checks for them.
// The result can be multiline.
func getText(comment *ast.CommentGroup, exclude []*regexp.Regexp) (s string) {
	if len(comment.List) > 0 && isSpecialBlock(comment.List[0].Text) {
		return ""
	}

	for _, c := range comment.List {
		text := c.Text
		isBlock := false
		if strings.HasPrefix(c.Text, "/*") {
			isBlock = true
			text = strings.TrimPrefix(text, "/*")
			text = strings.TrimSuffix(text, "*/")
		}
		for _, line := range strings.Split(text, "\n") {
			if isSpecialLine(line) {
				s += specialReplacer + "\n"
				continue
			}
			if !isBlock {
				line = strings.TrimPrefix(line, "//")
			}
			if matchAny(line, exclude) {
				s += specialReplacer + "\n"
				continue
			}
			s += line + "\n"
		}
	}
	if len(s) == 0 {
		return ""
	}
	return s[:len(s)-1] // trim last "\n"
}

// readFile reads file and returns its lines as strings.
func readFile(filename string) ([]string, error) {
	f, err := os.ReadFile(filepath.Clean(filename))
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	return strings.Split(string(f), "\n"), nil
}

// setDecl sets `decl` flag to comments which are declaration comments.
func setDecl(comments, decl []comment) {
	for _, d := range decl {
		for i, c := range comments {
			if d.start == c.start {
				comments[i].decl = true
				break
			}
		}
	}
}

// matchAny checks if string matches any of given regexps.
func matchAny(s string, rr []*regexp.Regexp) bool {
	for _, re := range rr {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func getFilename(fset *token.FileSet, file *ast.File) string {
	filename := fset.PositionFor(file.Pos(), true).Filename
	if !strings.HasSuffix(filename, ".go") {
		return fset.PositionFor(file.Pos(), false).Filename
	}

	return filename
}
