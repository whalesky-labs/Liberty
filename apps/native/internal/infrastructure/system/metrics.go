package system

import "errors"

var errProcessMetrics = errors.New("process_metrics_unavailable")

func processTree(root int, parents map[int]int) []int {
	if _, exists := parents[root]; !exists {
		return nil
	}
	children := make(map[int][]int)
	for pid, parent := range parents {
		children[parent] = append(children[parent], pid)
	}
	result := []int{root}
	seen := map[int]bool{root: true}
	for index := 0; index < len(result); index++ {
		for _, child := range children[result[index]] {
			if !seen[child] {
				seen[child] = true
				result = append(result, child)
			}
		}
	}
	return result
}
