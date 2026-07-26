"""Imports a sibling by bare name — valid because this directory is not a package."""

from corpus import read_source


def main(path):
    return read_source(path)
