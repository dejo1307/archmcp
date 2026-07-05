/* Runtime callback wiring inside a probe/init function.
 *  - obj->cb = fn  and  gc->x = &fn  field assignments  -> cache.go v9
 *  - cfg = (struct regmap_config){ .lock = fn };         -> cache.go v11
 * The assigned functions get an inbound call edge from the enclosing function so
 * they are not reported as dead code.
 */

static int xlp_gpio_set(void) { return 0; }
static int xlp_gpio_get(void) { return 0; }
static int mvebu_mask(void) { return 0; }
static int dio48e_regmap_lock(void) { return 0; }
static int dio48e_regmap_unlock(void) { return 0; }

static int probe(struct gpio_chip *gc)
{
	/* v9: function-pointer field assignments (plain and address-of). */
	gc->set = xlp_gpio_set;
	gc->get = &xlp_gpio_get;
	ct->chip.irq_mask = mvebu_mask;
	gc->ngpio = 32; /* plain data assignment: must NOT create an edge. */

	/* v11: in-body compound-literal designated initializers. */
	cfg = (struct regmap_config) {
		.reg_bits = 8,
		.lock     = dio48e_regmap_lock,
		.unlock   = dio48e_regmap_unlock,
	};

	return 0;
}
