/* Cross-file token-paste attribute macros (kernel configfs / sysfs style).
 * Defined here, invoked in drivers/attrs.c. The in-extractor macro pre-pass is
 * project-wide, so the pasted _show/_store callbacks are recovered even though
 * drivers/attrs.c does not literally #include this header. Exercises cache.go v13.
 */
#ifndef ENOLA_SAMPLE_ATTR_H
#define ENOLA_SAMPLE_ATTR_H

#define ATTR_PERM(_pfx, _name, _perm) \
	static struct configfs_attribute _pfx##attr_##_name = { \
		.ca_name  = __stringify(_name), \
		.ca_mode  = _perm, \
		.show     = _pfx##_name##_show, \
		.store    = _pfx##_name##_store, \
	}

#define ATTR(_pfx, _name) ATTR_PERM(_pfx, _name, 0644)

#endif /* ENOLA_SAMPLE_ATTR_H */
