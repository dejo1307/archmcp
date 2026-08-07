package com.example.core;

/** A Java type in the same package as the Scala sources, so the cross-language
 * package index is exercised: app's Scala import of it must resolve as internal. */
public final class LegacyId {
    private final long value;

    public LegacyId(long value) {
        this.value = value;
    }

    public long value() {
        return value;
    }
}
