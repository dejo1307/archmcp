package com.example.compute;

import org.junit.jupiter.api.Test;

// v58: this file lives under src/test/java/..., so its module fact is tagged
// module_role=test (production sources under src/main/java get module_role=production).
// Keeping test source sets out of the production population is what package-metrics
// relies on this prop for.
public class CalculatorTest {

    @Test
    public void testFib() {
        // Exercises production compute code from a test-role module.
    }
}
