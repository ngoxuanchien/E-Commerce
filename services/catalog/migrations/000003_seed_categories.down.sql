DELETE FROM categories WHERE parent_id IS NOT NULL;
DELETE FROM categories;
