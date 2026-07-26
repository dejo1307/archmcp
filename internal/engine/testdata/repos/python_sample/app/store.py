"""Imports the THIRD-PARTY sqlalchemy, not app/handlers/sqlalchemy.

The nested look-alike cannot satisfy a top-level import, so this dependency must
stay external and its call edge must be dropped — otherwise the library's calls
enter the graph as first-party and its module gains phantom coupling.
"""

import sqlalchemy


def make_table():
    return sqlalchemy.Table("t")
