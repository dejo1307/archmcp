/* Function references that live only inside #define replacement lists, invisible
 * to the AST, recovered by the macro-body scan.
 *  - IDENT( ... ) call position inside a #define      -> cache.go v12
 *  - .field = fn / = &fn value position inside #define -> cache.go v12
 * Also invokes the cross-file token-paste ATTR() macro from include/attr.h so the
 * pasted _show/_store callbacks are recovered -> cache.go v13.
 */

static int helper(void) { return 0; }
static int ____cmpxchg_u16(void) { return 0; }
static int ____cmpxchg_u32(void) { return 0; }
static int bank_set(void) { return 0; }
static int bank_get(void) { return 0; }
static int bank_irq(void) { return 0; }

/* v13: functions consumed by the token-pasted attribute macro (cfg_ + label). */
static int cfg_label_show(void) { return 0; }
static int cfg_label_store(void) { return 0; }

/* v12: call-position identifiers inside a macro body. */
#define DISPATCH(x) helper(x)
#define CMPX(p, size) (size == 2 ? ____cmpxchg_u16(p) : ____cmpxchg_u32(p))
#define CONST_ONLY 42

/* v12: value-position function pointers inside a macro-defined ops table. */
#define GPIO_BANK(_n) {            \
		.label  = _n,      \
		.set    = bank_set,       \
		.get    = bank_get,       \
		.to_irq = &bank_irq,      \
	}

/* v13: expands (via the project-wide #define pre-pass) to a struct whose
 * .show/.store point at cfg_label_show / cfg_label_store. */
ATTR(cfg_, label);
