/* File-scope registration macros.
 *  - module_init / fs_initcall / EXPORT_SYMBOL  -> cache.go v8
 *  - DEVICE_ATTR(name, mode, show, store) multi-arg -> cache.go v8
 *  - static DEFINE_SIMPLE_DEV_PM_OPS(...)           -> cache.go v10
 * All referenced functions are defined here so they resolve to real symbols and
 * the module-fact call edges land on them.
 */

static int chr_dev_init(void) { return 0; }
static int read_mem(void) { return 0; }
static int show_fn(void) { return 0; }
static int store_fn(void) { return 0; }
static int davinci_suspend(void) { return 0; }
static int davinci_resume(void) { return 0; }

/* v8: registration macros — the function-name args surface as module call edges. */
fs_initcall(chr_dev_init);
module_init(chr_dev_init);
EXPORT_SYMBOL(read_mem);

/* v8: DEVICE_ATTR mixes non-function args (name, 0644) with show/store fns. */
DEVICE_ATTR(name, 0644, show_fn, store_fn);

/* v10: a `static` qualifier turns the registration macro into a declaration
 * (macro_type_specifier); suspend/resume are recorded, the ops-table name is not.
 */
static DEFINE_SIMPLE_DEV_PM_OPS(davinci_pm_ops, davinci_suspend, davinci_resume);
