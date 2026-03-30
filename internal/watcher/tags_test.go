package watcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFolderTags_RootLevelFile(t *testing.T) {
	tags := FolderTags("/home/craig8/ideas/agreement.pdf", "/home/craig8/ideas")
	assert.Empty(t, tags, "root-level file should produce no folder tags")
}

func TestFolderTags_SingleSubdirectory(t *testing.T) {
	tags := FolderTags("/home/craig8/ideas/contracts/agreement.pdf", "/home/craig8/ideas")
	assert.Equal(t, []string{"contracts"}, tags)
}

func TestFolderTags_NestedSubdirectories(t *testing.T) {
	tags := FolderTags("/home/craig8/ideas/contracts/2024/agreement.pdf", "/home/craig8/ideas")
	assert.Equal(t, []string{"contracts", "2024"}, tags)
}

func TestFolderTags_SpecialCharactersStripped(t *testing.T) {
	tags := FolderTags("/root/docs/my@folder!/file.txt", "/root/docs")
	assert.Equal(t, []string{"myfolder"}, tags)
}

func TestFolderTags_SpacesToHyphens(t *testing.T) {
	tags := FolderTags("/root/docs/my folder/file.txt", "/root/docs")
	assert.Equal(t, []string{"my-folder"}, tags)
}

func TestFolderTags_CaseNormalization(t *testing.T) {
	tags := FolderTags("/root/docs/MyFolder/SubDir/file.txt", "/root/docs")
	assert.Equal(t, []string{"myfolder", "subdir"}, tags)
}

func TestFolderTags_Deduplication(t *testing.T) {
	// Two directory segments that normalize to the same tag
	tags := FolderTags("/root/docs/Notes/notes/file.txt", "/root/docs")
	assert.Equal(t, []string{"notes"}, tags)
}

func TestFolderTags_SpacesAndSpecialCharsCombined(t *testing.T) {
	tags := FolderTags("/root/docs/My Cool Folder!!/2024 Q1/file.txt", "/root/docs")
	assert.Equal(t, []string{"my-cool-folder", "2024-q1"}, tags)
}

func TestFolderTags_TrailingSlashOnRoot(t *testing.T) {
	tags := FolderTags("/root/docs/sub/file.txt", "/root/docs/")
	assert.Equal(t, []string{"sub"}, tags)
}

func TestFolderTags_FileAtRoot_EmptySlice(t *testing.T) {
	tags := FolderTags("/root/docs/file.txt", "/root/docs")
	// Must be empty (not nil), but principally length 0
	assert.Empty(t, tags)
}
