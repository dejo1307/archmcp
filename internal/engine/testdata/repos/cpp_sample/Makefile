# Realism only — the C/C++ extractor's Detect() keys off the presence of .c/.cpp
# sources, not this Makefile. Not used by the golden test build.
obj-m += reg.o probe.o macros.o board.o

CFLAGS += -Iinclude -Wall

all:
	$(CC) $(CFLAGS) -c drivers/reg.c drivers/probe.c drivers/macros.c drivers/board.c

clean:
	rm -f *.o
