package compose

import (
	"testing"
)

func BenchmarkToProjectName(b *testing.B) {
	helper := &ComposeHelper{
		Version: "1.29.2", // Old version format
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		helper.toProjectName("My_Project-Name")
	}
}

func BenchmarkToProjectName_NewFormat(b *testing.B) {
	helper := &ComposeHelper{
		Version: "2.0.0", // New version format
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		helper.toProjectName("My_Project-Name")
	}
}
