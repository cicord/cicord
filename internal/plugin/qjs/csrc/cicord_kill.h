// Included in every translation unit of quickjs.wasm (clang -include, see
// scripts/build-quickjs-wasm.sh). cicord_kill is set by Go (an atomic store into linear
// memory) to kill the VM: every C function entry (-finstrument-function-entry-bare) and the
// loops patched by the build script check it and trap.
//
// Every CICORD_YIELD_EVERY checks, the guest also calls into Go (cicord_yield). wazero's
// compiled code cannot be preempted by the Go scheduler: without a call into Go, a stop of
// the world (a garbage collection) started while the guest runs native code would wait for
// it, with every goroutine stopped, the one that kills the VM included, until the guest
// returned on its own. The call is a preemption point and checks the kill too.
#pragma once
#include <stdint.h>

extern volatile int32_t cicord_kill;
extern uint32_t cicord_ticks;
void cicord_yield(void);

#define CICORD_YIELD_EVERY 65536u

#define CICORD_KILL_CHECK()                                                        \
	do {                                                                           \
		if (__builtin_expect(cicord_kill != 0, 0))                                 \
			__builtin_trap();                                                      \
		if (__builtin_expect((++cicord_ticks & (CICORD_YIELD_EVERY - 1)) == 0, 0)) \
			cicord_yield();                                                        \
	} while (0)
