-- 1) Kategorilere sabit anahtar ve açıklama (yapay zekâ talimatı açıklamalardan üretilir)
ALTER TABLE categories ADD COLUMN key TEXT;
ALTER TABLE categories ADD COLUMN description TEXT NOT NULL DEFAULT '';

INSERT INTO categories (key, name, description, position) VALUES
  ('inbox',         'Gelen kutusu',       'Sistem sütunu: sınıflandırılmayı bekleyen ya da sınıflandırılamayan kartlar.', 0),
  ('purchase',      'Satın alım',         'Satın alınacak ya da sipariş edilecek şeyler. Örnek: süt al, kulaklık sipariş et.', 1),
  ('research',      'Araştırma',          'Bilgi toplama, öğrenme, karşılaştırma ya da ne yapılacağı için gerekli bilgisi verilmemiş belirsiz işler. Örnek: tatil için otel bak, X nedir öğren, bir hediye bul.', 2),
  ('communication', 'İletişim',           'Birini arama, mesaj ya da e-posta gönderme. Örnek: annemi ara, Ali''ye mail at.', 3),
  ('payment',       'Ödeme',              'Fatura, kira, vergi ve diğer para işleri. Örnek: elektrik faturasını öde.', 4),
  ('appointment',   'Randevu & Etkinlik', 'Tarihi ya da saati olan işler. Örnek: dişçi randevusu al, cuma toplantısı.', 5),
  ('work',          'İş',                 'Mesleki görevler. Örnek: sunumu bitir, kod incelemesi.', 6),
  ('home',          'Ev & Kişisel',       'Ev işleri, sağlık, spor, kişisel bakım. Örnek: çamaşır yıka, spora git.', 7),
  ('other',         'Diğer',              'Hiçbir kategoriye uymayanlar.', 8);

-- 2) Todo'lara durum, açıklama, sınıflandırma durumu
ALTER TABLE todos ADD COLUMN status TEXT NOT NULL DEFAULT 'todo'
  CHECK (status IN ('todo', 'in_progress', 'done'));
ALTER TABLE todos ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE todos ADD COLUMN classification TEXT NOT NULL DEFAULT 'pending'
  CHECK (classification IN ('pending', 'done', 'failed', 'manual'));

-- 3) Eski durum sütunları (anahtarı olmayan kategoriler; position 1/2/3) → durum
UPDATE todos SET status = CASE
  (SELECT c.position FROM categories c WHERE c.id = todos.category_id AND c.key IS NULL)
  WHEN 2 THEN 'in_progress' WHEN 3 THEN 'done' ELSE 'todo' END;

-- 4) Tüm kartlar Gelen kutusuna; sıra: eski sütun sırası, sonra eski sıra
UPDATE todos SET
  category_id = (SELECT id FROM categories WHERE key = 'inbox'),
  position    = ranked.rn
FROM (SELECT t.id, ROW_NUMBER() OVER (ORDER BY c.position, t.position, t.id) - 1 AS rn
      FROM todos t JOIN categories c ON c.id = t.category_id) AS ranked
WHERE todos.id = ranked.id;

-- 5) Eski 3 kategori silinir; anahtar benzersiz
DELETE FROM categories WHERE key IS NULL;
CREATE UNIQUE INDEX idx_categories_key ON categories(key);
CREATE INDEX idx_todos_classification ON todos(classification);
