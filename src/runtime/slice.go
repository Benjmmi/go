// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"runtime/internal/math"
	"runtime/internal/sys"
	"unsafe"
)

type slice struct {
	array unsafe.Pointer // 底层数组的地址
	len   int            // slice 的长度
	cap   int            // slice 的容量
}

// A notInHeapSlice is a slice backed by go:notinheap memory.
type notInHeapSlice struct {
	array *notInHeap
	len   int
	cap   int
}

func panicmakeslicelen() {
	panic(errorString("makeslice: len out of range"))
}

func panicmakeslicecap() {
	panic(errorString("makeslice: cap out of range"))
}

// makeslicecopy allocates a slice of "tolen" elements of type "et",
// then copies "fromlen" elements of type "et" into that new allocation from "from".
// makeslicecopy 分配一个 "et "类型的 "tolen "个元素的 slice，然后将 "et "类型的 "fromlen "个元素从 "from "复制到这个新的内存。
func makeslicecopy(et *_type, tolen int, fromlen int, from unsafe.Pointer) unsafe.Pointer {
	var tomem, copymem uintptr
	if uintptr(tolen) > uintptr(fromlen) {
		var overflow bool
		tomem, overflow = math.MulUintptr(et.size, uintptr(tolen))
		if overflow || tomem > maxAlloc || tolen < 0 {
			panicmakeslicelen()
		}
		copymem = et.size * uintptr(fromlen)
	} else {
		// 内存整理的时候才会使用？
		// fromlen is a known good length providing and equal or greater than tolen,
		// thereby making tolen a good slice length too as from and to slices have the
		// same element width.
		// fromlen是一个已知的正常的长度，它等于或大于tolen，从而使tolen也是一个好的切片长度，
		// 因为from和to切片具有相同的元素宽度。
		tomem = et.size * uintptr(tolen)
		copymem = tomem
	}

	var to unsafe.Pointer
	if et.ptrdata == 0 { // 非指针类型
		to = mallocgc(tomem, nil, false)
		if copymem < tomem {
			// 清空内存，没有写屏障
			memclrNoHeapPointers(add(to, copymem), tomem-copymem)
		}
	} else {
		// Note: can't use rawmem (which avoids zeroing of memory), because then GC can scan uninitialized memory.
		// 注意：不能使用rawmem（它避免了内存清零），因为这样GC就可以扫描未初始化的内存。
		to = mallocgc(tomem, et, true)
		if copymem > 0 && writeBarrier.enabled {
			// Only shade the pointers in old.array since we know the destination slice to
			// only contains nil pointers because it has been cleared during alloc.
			// 只对“old.array”中的指针进行着色处理，因为我们知道目标切片只包含零指针，因为它在分配期间已被清除。
			bulkBarrierPreWriteSrcOnly(uintptr(to), uintptr(from), copymem)
		}
	}

	if raceenabled {
		callerpc := getcallerpc()
		pc := abi.FuncPCABIInternal(makeslicecopy)
		racereadrangepc(from, copymem, callerpc, pc)
	}
	if msanenabled {
		msanread(from, copymem)
	}
	if asanenabled {
		asanread(from, copymem)
	}

	memmove(to, from, copymem)

	return to
}

func makeslice(et *_type, len, cap int) unsafe.Pointer {
	mem, overflow := math.MulUintptr(et.size, uintptr(cap))
	if overflow || mem > maxAlloc || len < 0 || len > cap { // cap 总是大于len。len 永远大于0。可分配内存不可以大于最大内存
		// NOTE: Produce a 'len out of range' error instead of a
		// 'cap out of range' error when someone does make([]T, bignumber).
		// 'cap out of range' is true too, but since the cap is only being
		// supplied implicitly, saying len is clearer.
		// See golang.org/issue/4085.
		mem, overflow := math.MulUintptr(et.size, uintptr(len))
		if overflow || mem > maxAlloc || len < 0 {
			panicmakeslicelen()
		}
		panicmakeslicecap()
	}
	// 分配一段内存，mem:需要内存大小，et:类型内存结构，true:需要清零
	return mallocgc(mem, et, true)
}

func makeslice64(et *_type, len64, cap64 int64) unsafe.Pointer {
	len := int(len64)
	if int64(len) != len64 {
		panicmakeslicelen()
	}

	cap := int(cap64)
	if int64(cap) != cap64 {
		panicmakeslicecap()
	}

	return makeslice(et, len, cap)
}

// This is a wrapper over runtime/internal/math.MulUintptr,
// so the compiler can recognize and treat it as an intrinsic.
func mulUintptr(a, b uintptr) (uintptr, bool) {
	return math.MulUintptr(a, b)
}

// Keep this code in sync with cmd/compile/internal/walk/builtin.go:walkUnsafeSlice
func unsafeslice(et *_type, ptr unsafe.Pointer, len int) {
	if len < 0 {
		panicunsafeslicelen()
	}

	mem, overflow := math.MulUintptr(et.size, uintptr(len))
	if overflow || mem > -uintptr(ptr) {
		if ptr == nil {
			panicunsafeslicenilptr()
		}
		panicunsafeslicelen()
	}
}

// Keep this code in sync with cmd/compile/internal/walk/builtin.go:walkUnsafeSlice
func unsafeslice64(et *_type, ptr unsafe.Pointer, len64 int64) {
	len := int(len64)
	if int64(len) != len64 {
		panicunsafeslicelen()
	}
	unsafeslice(et, ptr, len)
}

func unsafeslicecheckptr(et *_type, ptr unsafe.Pointer, len64 int64) {
	unsafeslice64(et, ptr, len64)

	// Check that underlying array doesn't straddle multiple heap objects.
	// unsafeslice64 has already checked for overflow.
	if checkptrStraddles(ptr, uintptr(len64)*et.size) {
		throw("checkptr: unsafe.Slice result straddles multiple allocations")
	}
}

func panicunsafeslicelen() {
	panic(errorString("unsafe.Slice: len out of range"))
}

func panicunsafeslicenilptr() {
	panic(errorString("unsafe.Slice: ptr is nil and len is not zero"))
}

// growslice handles slice growth during append.
// It is passed the slice element type, the old slice, and the desired new minimum capacity,
// and it returns a new slice with at least that capacity, with the old data
// copied into it.
// The new slice's length is set to the old slice's length,
// NOT to the new requested capacity.
// This is for codegen convenience. The old slice's length is used immediately
// to calculate where to write new values during an append.
// TODO: When the old backend is gone, reconsider this decision.
// The SSA backend might prefer the new length or to return only ptr/cap and save stack space.
//
// growslice在追加过程中处理分片的增长。
// 它被传递给分片元素类型、旧分片和所需的新的最小容量，并且它返回一个至少具有该容量的新分片，并将旧数据复制到其中。
// 新 slice 的长度被设置为旧 solice 的长度，而不是新要求的容量。
// 这是为了编码的方便。在追加过程中，旧片段的长度被立即用来计算在哪里写入新值。
// TODO: 当旧的后端消失后，重新考虑这个决定。
// SSA后端可能更喜欢新的长度或者只返回ptr/cap并节省堆栈空间。
func growslice(et *_type, old slice, cap int) slice {
	if raceenabled {
		callerpc := getcallerpc()
		racereadrangepc(old.array, uintptr(old.len*int(et.size)), callerpc, abi.FuncPCABIInternal(growslice))
	}
	if msanenabled {
		msanread(old.array, uintptr(old.len*int(et.size)))
	}
	if asanenabled {
		asanread(old.array, uintptr(old.len*int(et.size)))
	}

	if cap < old.cap {
		panic(errorString("growslice: cap out of range"))
	}

	if et.size == 0 {
		// append should not create a slice with nil pointer but non-zero len.
		// We assume that append doesn't need to preserve old.array in this case.
		return slice{unsafe.Pointer(&zerobase), old.len, cap}
	}

	newcap := old.cap
	doublecap := newcap + newcap
	if cap > doublecap {
		newcap = cap
	} else {
		const threshold = 256    // 阈值
		if old.cap < threshold { // 就的容量小于阈值
			newcap = doublecap // 就翻倍增长
		} else {
			// Check 0 < newcap to detect overflow
			// and prevent an infinite loop.
			// 检查0 < newcap以检测溢出并防止无限循环。
			for 0 < newcap && newcap < cap {
				// Transition from growing 2x for small slices
				// to growing 1.25x for large slices. This formula
				// gives a smooth-ish transition between the two.
				// 从小片的增长2倍过渡到大片的增长1.25倍。这个公式在两者之间给出了一个平滑的过渡。
				newcap += (newcap + 3*threshold) / 4
			}
			// Set newcap to the requested cap when
			// the newcap calculation overflowed.
			if newcap <= 0 {
				newcap = cap
			}
		}
	}

	var overflow bool
	var lenmem, newlenmem, capmem uintptr
	// Specialize for common values of et.size.
	// For 1 we don't need any division/multiplication.
	// For goarch.PtrSize, compiler will optimize division/multiplication into a shift by a constant.
	// For powers of 2, use a variable shift.
	switch {
	case et.size == 1:
		lenmem = uintptr(old.len)
		newlenmem = uintptr(cap)
		capmem = roundupsize(uintptr(newcap))
		overflow = uintptr(newcap) > maxAlloc
		newcap = int(capmem)
	case et.size == goarch.PtrSize:
		lenmem = uintptr(old.len) * goarch.PtrSize
		newlenmem = uintptr(cap) * goarch.PtrSize
		capmem = roundupsize(uintptr(newcap) * goarch.PtrSize)
		overflow = uintptr(newcap) > maxAlloc/goarch.PtrSize
		newcap = int(capmem / goarch.PtrSize)
	case isPowerOfTwo(et.size):
		var shift uintptr
		if goarch.PtrSize == 8 {
			// Mask shift for better code generation.
			shift = uintptr(sys.Ctz64(uint64(et.size))) & 63
		} else {
			shift = uintptr(sys.Ctz32(uint32(et.size))) & 31
		}
		lenmem = uintptr(old.len) << shift
		newlenmem = uintptr(cap) << shift
		capmem = roundupsize(uintptr(newcap) << shift)
		overflow = uintptr(newcap) > (maxAlloc >> shift)
		newcap = int(capmem >> shift)
	default:
		lenmem = uintptr(old.len) * et.size
		newlenmem = uintptr(cap) * et.size
		capmem, overflow = math.MulUintptr(et.size, uintptr(newcap))
		capmem = roundupsize(capmem)
		newcap = int(capmem / et.size)
	}

	// The check of overflow in addition to capmem > maxAlloc is needed
	// to prevent an overflow which can be used to trigger a segfault
	// on 32bit architectures with this example program:
	//
	// type T [1<<27 + 1]int64
	//
	// var d T
	// var s []T
	//
	// func main() {
	//   s = append(s, d, d, d, d)
	//   print(len(s), "\n")
	// }
	if overflow || capmem > maxAlloc {
		panic(errorString("growslice: cap out of range"))
	}

	var p unsafe.Pointer
	if et.ptrdata == 0 {
		p = mallocgc(capmem, nil, false)
		// The append() that calls growslice is going to overwrite from old.len to cap (which will be the new length).
		// Only clear the part that will not be overwritten.
		memclrNoHeapPointers(add(p, newlenmem), capmem-newlenmem)
	} else {
		// Note: can't use rawmem (which avoids zeroing of memory), because then GC can scan uninitialized memory.
		p = mallocgc(capmem, et, true)
		if lenmem > 0 && writeBarrier.enabled {
			// Only shade the pointers in old.array since we know the destination slice p
			// only contains nil pointers because it has been cleared during alloc.
			bulkBarrierPreWriteSrcOnly(uintptr(p), uintptr(old.array), lenmem-et.size+et.ptrdata)
		}
	}
	memmove(p, old.array, lenmem)

	return slice{p, old.len, newcap}
}

func isPowerOfTwo(x uintptr) bool {
	return x&(x-1) == 0
}

// slicecopy is used to copy from a string or slice of pointerless elements into a slice.
func slicecopy(toPtr unsafe.Pointer, toLen int, fromPtr unsafe.Pointer, fromLen int, width uintptr) int {
	if fromLen == 0 || toLen == 0 {
		return 0
	}

	n := fromLen
	if toLen < n {
		n = toLen
	}

	if width == 0 {
		return n
	}

	size := uintptr(n) * width
	if raceenabled {
		callerpc := getcallerpc()
		pc := abi.FuncPCABIInternal(slicecopy)
		racereadrangepc(fromPtr, size, callerpc, pc)
		racewriterangepc(toPtr, size, callerpc, pc)
	}
	if msanenabled {
		msanread(fromPtr, size)
		msanwrite(toPtr, size)
	}
	if asanenabled {
		asanread(fromPtr, size)
		asanwrite(toPtr, size)
	}

	if size == 1 { // common case worth about 2x to do here
		// TODO: is this still worth it with new memmove impl?
		*(*byte)(toPtr) = *(*byte)(fromPtr) // known to be a byte pointer
	} else {
		memmove(toPtr, fromPtr, size)
	}
	return n
}
