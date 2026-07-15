package com.example.spi;

// Registered only in META-INF/dubbo/internal/com.example.spi.Strategy and loaded by
// name — no in-code caller. The SPI service-file fold keeps it off the dead-code list.
public class RandomStrategy implements Strategy {
    @Override
    public String name() {
        return "random";
    }
}
