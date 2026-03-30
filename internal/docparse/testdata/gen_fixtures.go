//go:build ignore

// gen_fixtures generates test fixture files for docparse tests.
// Run: go run testdata/gen_fixtures.go
package main

import (
	"fmt"
	"os"

	"github.com/fumiama/go-docx"
)

func main() {
	generateDOCX()
	fmt.Println("Generated testdata/sample.docx")
}

func generateDOCX() {
	doc := docx.New()
	doc.AddParagraph().AddText("Hello from OpenBrain")
	doc.AddParagraph().AddText("This is a test document for ingestion.")
	doc.AddParagraph().AddText("It contains three paragraphs of text.")

	f, err := os.Create("testdata/sample.docx")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	_, err = doc.WriteTo(f)
	if err != nil {
		panic(err)
	}
}
