// Package subnormaltest finds subnormal numbers in a processor's state, for
// tests that check that decaying state never stalls in them.
package subnormaltest

import (
	"fmt"
	"math"
	"reflect"
)

// Find walks v (a pointer to a processor, usually) through structs, arrays,
// slices and pointers, including unexported fields, and returns the path of
// every float32 or float64 that holds a subnormal number, up to limit paths.
func Find(v any, limit int) []string {
	var found []string
	seen := map[uintptr]bool{}
	var walk func(value reflect.Value, path string)
	walk = func(value reflect.Value, path string) {
		if len(found) >= limit {
			return
		}
		switch value.Kind() {
		case reflect.Float32:
			bits := math.Float32bits(float32(value.Float()))
			if bits&0x7f800000 == 0 && bits&0x7fffffff != 0 {
				found = append(found, path)
			}
		case reflect.Float64:
			bits := math.Float64bits(value.Float())
			if bits&0x7ff0000000000000 == 0 && bits&0x7fffffffffffffff != 0 {
				found = append(found, path)
			}
		case reflect.Pointer:
			if value.IsNil() || seen[value.Pointer()] {
				return
			}
			seen[value.Pointer()] = true
			walk(value.Elem(), path)
		case reflect.Struct:
			for i := range value.NumField() {
				walk(value.Field(i), path+"."+value.Type().Field(i).Name)
			}
		case reflect.Array, reflect.Slice:
			for i := range value.Len() {
				walk(value.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(reflect.ValueOf(v), "")
	return found
}
