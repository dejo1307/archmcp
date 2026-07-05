/* ARM machine_desc pattern: a struct opened by a macro (DT_MACHINE_START) and
 * closed by another (MACHINE_END). tree-sitter renders the `.field = fn` lines as
 * a file-scope ERROR node / bare assignment_expression + field_expression debris;
 * the callbacks are salvaged from that region.
 *   -> cache.go v15 (file-scope ERROR region)
 *   -> cache.go v16 (assignment_expression/field_expression fragments)
 *   -> cache.go v17 (full-tree `.field = fn` macro-struct debris)
 * The leading declarations + the call-valued `.smp` field reproduce the exact
 * shape that makes tree-sitter scatter the blocks rather than emit one clean ERROR.
 */

static void omap_reserve(void) {}
static void omap_generic_init(void) {}
static void omap2xxx_restart(void) {}
static void mvebu_dt_init(void) {}
static const char *const compat[] = { "ti,omap2420", 0 };

DT_MACHINE_START(OMAP242X_DT, "Generic OMAP2420")
	.smp		= smp_ops(omap_smp_ops),
	.reserve	= omap_reserve,
	.init_machine	= omap_generic_init,
	.restart	= omap2xxx_restart,
MACHINE_END

DT_MACHINE_START(OMAP243X_DT, "Generic OMAP2430")
	.init_machine	= mvebu_dt_init,
MACHINE_END
