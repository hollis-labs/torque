import importlib.util
from pathlib import Path
import sqlite3
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('migration', Path(__file__).with_name('consolidate-forum-namespaces.py'))
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class MigrationTest(unittest.TestCase):
    def test_preserves_data_backup_and_idempotence(self):
        with tempfile.TemporaryDirectory() as tmp:
            path, backup = Path(tmp)/'tether.db', Path(tmp)/'backup.db'
            with sqlite3.connect(path) as db:
                db.executescript('''CREATE TABLE forums(id TEXT PRIMARY KEY,namespace TEXT,category_id TEXT,metadata TEXT);
                CREATE TABLE forum_categories(id TEXT PRIMARY KEY,namespace TEXT,description TEXT);
                CREATE TABLE posts(id TEXT,forum_id TEXT,body TEXT);
                CREATE TABLE grants(forum_id TEXT,principal TEXT);
                INSERT INTO forums VALUES ('work','app/torque/forum/work','cat','keep'),('external','app/other','','other');
                INSERT INTO forum_categories VALUES ('cat','app/torque/forum/work','description');
                INSERT INTO posts VALUES ('p1','work','retain message');
                INSERT INTO grants VALUES ('work','agent');''')
                before = '\n'.join(db.iterdump())
            self.assertFalse(migration.consolidate(path)['applied'])
            with sqlite3.connect(path) as db:
                self.assertEqual(before, '\n'.join(db.iterdump()))
            result = migration.consolidate(path, backup)
            self.assertEqual(result['changed'], {'forums': 1, 'forum_categories': 1})
            with sqlite3.connect(backup) as db:
                self.assertEqual(before, '\n'.join(db.iterdump()))
            with sqlite3.connect(path) as db:
                after = '\n'.join(db.iterdump())
                self.assertEqual(before.replace('app/torque/forum/work', migration.ROOT), after)
            self.assertEqual(migration.consolidate(path)['changed'], {'forums': 0, 'forum_categories': 0})
            with self.assertRaises(FileExistsError):
                migration.consolidate(path, backup)

    def test_rejects_cross_application_category(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'tether.db'
            with sqlite3.connect(path) as db:
                db.executescript('''CREATE TABLE forums(id TEXT,namespace TEXT,category_id TEXT);
                CREATE TABLE forum_categories(id TEXT,namespace TEXT);
                INSERT INTO forums VALUES ('work','app/torque/forum/work','other');
                INSERT INTO forum_categories VALUES ('other','app/other');''')
            with self.assertRaises(ValueError):
                migration.consolidate(path)


if __name__ == '__main__':
    unittest.main()
