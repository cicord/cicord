// Included in every translation unit of quickjs.wasm (clang -include, see
// scripts/build-quickjs-wasm.sh). cicord_kill is set by Go (an atomic store into linear
// memory) to kill the VM: every C function entry (-finstrument-function-entry-bare) and the
// loops patched by the build script check it and trap.
#pragma once
#include <stdint.h>

extern volatile int32_t cicord_kill;

#define CICORD_KILL_CHECK()                       \
	do {                                          \
		if (__builtin_expect(cicord_kill != 0, 0)) \
			__builtin_trap();                     \
	} while (0)
