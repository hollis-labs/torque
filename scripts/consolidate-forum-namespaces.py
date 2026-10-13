#!/usr/bin/env python3
"""One-off Tether SQLite maintenance; dry-run unless --apply is supplied."""
import argparse
import json
import os
from pathlib import Path
import sqlite3

ROOT = 'app/torque/forum/torque'
PREFIX = 'app/torque/forum/'
TABLES = ('forum_categories', 'forums')


def consolidate(database, backup=None):
    database = Path(database).resolve(strict=True)
    apply = backup is not None
    with sqlite3.connect(database.as_uri() + ('?mode=rw' if apply else '?mode=ro'), uri=True, timeout=30) as db:
        # Serialize maintenance with Tether's writer. Backup via a separate reader
        # while this lock is held, before making any changes.
        db.execute('BEGIN IMMEDIATE' if apply else 'BEGIN')
        rows = {table: db.execute(f'SELECT id,namespace FROM {table} WHERE namespace LIKE ? AND namespace != ? ORDER BY id', (PREFIX+'%', ROOT)).fetchall() for table in TABLES}
        category_scopes = dict(db.execute('SELECT id,namespace FROM forum_categories'))
        for forum_id, namespace, category_id in db.execute('SELECT id,namespace,category_id FROM forums WHERE category_id != ""'):
            category_ns = category_scopes.get(category_id)
            future_forum = ROOT if namespace.startswith(PREFIX) else namespace
            future_category = ROOT if category_ns and category_ns.startswith(PREFIX) else category_ns
            if future_forum != future_category:
                raise ValueError(f'Inconsistent category relationship: {forum_id} -> {category_id}')
        if apply:
            backup = Path(backup)
            # Exclusive creation prevents replacing an earlier rollback snapshot.
            fd = os.open(backup, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            os.close(fd)
            with sqlite3.connect(database.as_uri()+'?mode=ro',uri=True) as source, sqlite3.connect(backup) as dest:
                source.backup(dest)
            journal = Path(str(backup)+'.namespaces.json')
            fd = os.open(journal, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with os.fdopen(fd,'w') as out:
                json.dump({'root': ROOT, 'original_namespaces': rows},out,indent=2)
                out.flush()
                os.fsync(out.fileno())
            for table in TABLES:
                db.executemany(f'UPDATE {table} SET namespace=? WHERE id=? AND namespace=?', [(ROOT, row_id, old) for row_id,old in rows[table]])
            db.commit()
        else:
            db.rollback()
    return {'applied': apply, 'root': ROOT, 'changed': {table: len(values) for table,values in rows.items()}}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('database', type=Path)
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--backup', type=Path, help='New SQLite backup path; also writes a namespace rollback journal')
    args = parser.parse_args()
    if args.apply != bool(args.backup):
        parser.error('--apply and --backup must be supplied together')
    print(json.dumps(consolidate(args.database, args.backup)))
