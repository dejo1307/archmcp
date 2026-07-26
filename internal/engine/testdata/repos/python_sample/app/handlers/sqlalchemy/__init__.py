"""A subpackage that happens to share its name with a third-party library.

Reachable only as app.handlers.sqlalchemy, never as a bare `import sqlalchemy`,
because its parent holds an __init__.py. Suffix matching used to let a directory
like this capture the real library's imports.
"""
