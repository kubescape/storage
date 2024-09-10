package dynamicpathdetectortests

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

func BenchmarkAnalyzePath(b *testing.B) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)
	paths := generateMixedPaths(10000, 0) // 0 means use default mixed lengths

	identifier := "test"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := paths[i%len(paths)]
		_, err := analyzer.AnalyzePath(path, identifier)
		if err != nil {
			b.Fatalf("Error analyzing path: %v", err)
		}
	}
}

func BenchmarkAnalyzePathWithDifferentLengths(b *testing.B) {
	pathLengths := []int{1, 3, 5, 10, 20, 50, 100}

	for _, length := range pathLengths {
		b.Run(fmt.Sprintf("PathLength-%d", length), func(b *testing.B) {
			analyzer := dynamicpathdetector.NewPathAnalyzer(100)
			paths := generateMixedPaths(10000, length)
			identifier := "test"

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				path := paths[i%len(paths)]
				_, err := analyzer.AnalyzePath(path, identifier)
				if err != nil {
					b.Fatalf("Error analyzing path: %v", err)
				}
			}

		})
	}
}

func generateMixedPaths(count int, fixedLength int) []string {
	paths := make([]string, count)
	staticSegments := []string{"users", "profile", "settings", "api", "v1", "posts", "organizations", "departments", "employees", "projects", "tasks", "categories", "subcategories", "items", "articles"}

	for i := 0; i < count; i++ {
		if fixedLength > 0 {
			segments := make([]string, fixedLength)
			for j := 0; j < fixedLength; j++ {
				if rand.Float32() < 0.2 { // 20% chance of dynamic segment
					prefix := staticSegments[rand.Intn(len(staticSegments))]
					// Generate a value > 100 to ensure it's considered dynamic
					dynamicValue := rand.Intn(10000) + 101
					segments[j] = fmt.Sprintf("%s_%d", prefix, dynamicValue)
				} else { // 80% chance of static segment
					segments[j] = staticSegments[rand.Intn(len(staticSegments))]
				}
			}
			paths[i] = "/" + strings.Join(segments, "/")
		} else {
			// Use the original mixed path generation logic for variable length paths
			switch rand.Intn(6) {
			case 0:
				paths[i] = "/users/profile/settings"
			case 1:
				paths[i] = fmt.Sprintf("/users/%d/profile", i%200)
			case 2:
				paths[i] = fmt.Sprintf("/api/v1/users/%d/posts/%d", i%200, i%150)
			case 3:
				paths[i] = fmt.Sprintf("/organizations/%d/departments/%d/employees/%d/projects/%d/tasks/%d",
					i%100, i%50, i%1000, i%30, i%200)
			case 4:
				repeatedSegment := fmt.Sprintf("%d", i%150)
				paths[i] = fmt.Sprintf("/categories/%s/subcategories/%s/items/%s",
					repeatedSegment, repeatedSegment, repeatedSegment)
			case 5:
				paths[i] = fmt.Sprintf("/articles/%d/%s-%s-%s",
					i%100,
					generateRandomString(5),
					generateRandomString(7),
					generateRandomString(5))
			}
		}
	}
	return paths
}

// Helper function to generate random strings
func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}
	return string(result)
}
