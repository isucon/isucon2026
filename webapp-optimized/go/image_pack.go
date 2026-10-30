package isureal

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jmoiron/sqlx"
	"golang.org/x/sync/errgroup"
)

// imagePack holds the bytes of the initial data's JPEG images on the app's
// local disk (ISUREAL_IMAGE_DIR/initial.pack), grouped by owner in creation
// order, so one user's history is one contiguous read and the database's
// cache is left to the rows. The initial data's images never change; whether
// an image may be served is still decided by the database. `isureal
// pack-images` builds the files once from the database.
type imagePack struct {
	file    *os.File
	owners  []string
	entries []packEntry // ordered by id
}

type packEntry struct {
	id     [16]byte
	owner  uint32
	length uint32
	offset uint64
}

const packMagic = "ISRPACK1"

// pack is nil when there is no pack for this database.
var pack *imagePack

func packPaths(dir string) (data, index string) {
	return filepath.Join(dir, "initial.pack"), filepath.Join(dir, "initial.idx")
}

func parseImageID(id string) ([16]byte, bool) {
	if len(id) != 36 {
		return [16]byte{}, false
	}
	u, err := uuid.Parse(id)
	return u, err == nil
}

// openImagePack loads the index when its initial data boundary matches the
// database's.
func openImagePack(ctx context.Context, db sqlx.QueryerContext, dir string) (*imagePack, error) {
	dataPath, indexPath := packPaths(dir)
	index, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}
	r := bytes.NewReader(index)
	magic := make([]byte, len(packMagic))
	var boundary int64
	var owners uint32
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != packMagic {
		return nil, errors.New("image pack: bad index")
	}
	if err := binary.Read(r, binary.LittleEndian, &boundary); err != nil {
		return nil, err
	}
	var current time.Time
	if err := sqlx.GetContext(ctx, db, &current, `SELECT created_at FROM initial_data_boundary`); err != nil {
		return nil, err
	}
	if current.UnixMicro() != boundary {
		return nil, fmt.Errorf("image pack: built for another initial data (%d, database %d)", boundary, current.UnixMicro())
	}
	if err := binary.Read(r, binary.LittleEndian, &owners); err != nil {
		return nil, err
	}
	p := &imagePack{owners: make([]string, owners)}
	for i := range p.owners {
		var n uint16
		if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
			return nil, err
		}
		name := make([]byte, n)
		if _, err := io.ReadFull(r, name); err != nil {
			return nil, err
		}
		p.owners[i] = string(name)
	}
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return nil, err
	}
	p.entries = make([]packEntry, count)
	record := make([]byte, 32)
	for i := range p.entries {
		if _, err := io.ReadFull(r, record); err != nil {
			return nil, err
		}
		e := &p.entries[i]
		copy(e.id[:], record[:16])
		e.owner = binary.LittleEndian.Uint32(record[16:])
		e.length = binary.LittleEndian.Uint32(record[20:])
		e.offset = binary.LittleEndian.Uint64(record[24:])
	}
	if p.file, err = os.Open(dataPath); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *imagePack) lookup(id string) (packEntry, bool) {
	if p == nil {
		return packEntry{}, false
	}
	key, ok := parseImageID(id)
	if !ok {
		return packEntry{}, false
	}
	i, found := slices.BinarySearchFunc(p.entries, key, func(e packEntry, k [16]byte) int {
		return bytes.Compare(e.id[:], k[:])
	})
	if !found {
		return packEntry{}, false
	}
	return p.entries[i], true
}

func (p *imagePack) read(e packEntry) ([]byte, error) {
	data := make([]byte, e.length)
	_, err := p.file.ReadAt(data, int64(e.offset))
	return data, err
}

// readMany reads the images of ids that are in the pack. Neighbouring images
// (one owner's) are read together.
func (p *imagePack) readMany(ids []string) (map[string][]byte, error) {
	type want struct {
		id string
		e  packEntry
	}
	var wants []want
	for _, id := range ids {
		if e, ok := p.lookup(id); ok {
			wants = append(wants, want{id, e})
		}
	}
	slices.SortFunc(wants, func(a, b want) int { return cmp.Compare(a.e.offset, b.e.offset) })
	data := make(map[string][]byte, len(wants))
	for start := 0; start < len(wants); {
		end := start + 1
		limit := wants[start].e.offset + uint64(wants[start].e.length)
		for end < len(wants) && wants[end].e.offset <= limit+64<<10 && limit-wants[start].e.offset < 16<<20 {
			limit = max(limit, wants[end].e.offset+uint64(wants[end].e.length))
			end++
		}
		span := make([]byte, limit-wants[start].e.offset)
		if _, err := p.file.ReadAt(span, int64(wants[start].e.offset)); err != nil {
			return nil, err
		}
		for _, w := range wants[start:end] {
			from := w.e.offset - wants[start].e.offset
			data[w.id] = span[from : from+uint64(w.e.length) : from+uint64(w.e.length)]
		}
		start = end
	}
	return data, nil
}

// loadPackImageObject decides like loadImageObject whether an image of the
// pack is live, without touching image_objects: the owner is known.
func loadPackImageObject(ctx context.Context, db sqlx.QueryerContext, id string, e packEntry) (imageObject, error) {
	var object imageObject
	err := sqlx.GetContext(ctx, db, &object, `
      SELECT u.secret_key_hash, (u.profile_image_id = $1 OR EXISTS (
          SELECT 1 FROM posts p WHERE p.image_id=$1 AND p.author_id=u.id AND p.deleted_at IS NULL
        ) OR EXISTS (
          SELECT 1 FROM realmojis r
          JOIN posts p ON p.id=r.post_id AND p.deleted_at IS NULL
          JOIN users author ON author.id=p.author_id AND author.status='active'
          WHERE r.image_id=$1 AND r.reactor_id=u.id AND r.replaced_at IS NULL AND r.deleted_at IS NULL
        )) IS TRUE AS live
      FROM users u WHERE u.id=$2 AND u.status='active'
    `, id, pack.owners[e.owner])
	object.MediaType = "image/jpeg"
	return object, err
}

// BuildImagePack writes the pack of the initial data's JPEG images into dir.
// The partitions are read in parallel and spread into buckets by owner; each
// bucket is then sorted in memory and appended to the pack.
func BuildImagePack(ctx context.Context, config *Config, dir string) error {
	const buckets = 128
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var boundary time.Time
	if err := conn.QueryRow(ctx, `SELECT created_at FROM initial_data_boundary`).Scan(&boundary); err != nil {
		return err
	}
	rows, err := conn.Query(ctx, `SELECT c.relname::text FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
      WHERE i.inhparent = 'image_objects'::regclass ORDER BY c.relname`)
	if err != nil {
		return err
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		tables = []string{"image_objects"}
	}

	tmp := filepath.Join(dir, "pack.tmp")
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	files := make([]*os.File, buckets)
	writers := make([]*bufio.Writer, buckets)
	locks := make([]sync.Mutex, buckets)
	for i := range files {
		if files[i], err = os.Create(filepath.Join(tmp, strconv.Itoa(i))); err != nil {
			return err
		}
		defer files[i].Close()
		writers[i] = bufio.NewWriterSize(files[i], 256<<10)
	}
	var total sync.Mutex
	images, bytesRead := 0, 0
	started := time.Now()
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for _, table := range tables {
		group.Go(func() error {
			c, err := pgx.Connect(gctx, config.DatabaseDSN())
			if err != nil {
				return err
			}
			defer c.Close(context.Background())
			rows, err := c.Query(gctx, `SELECT id, owner_user_id, created_at, data FROM `+pgx.Identifier{table}.Sanitize()+`
              WHERE created_at <= $1 AND media_type = 'image/jpeg'`, boundary)
			if err != nil {
				return err
			}
			defer rows.Close()
			var head [8]byte
			n, size := 0, 0
			for rows.Next() {
				var id, owner string
				var at time.Time
				var data []byte
				if err := rows.Scan(&id, &owner, &at, &data); err != nil {
					return err
				}
				if _, ok := parseImageID(id); !ok || len(owner) > 0xffff {
					continue
				}
				h := fnv.New32a()
				h.Write([]byte(owner))
				b := h.Sum32() % buckets
				locks[b].Lock()
				w := writers[b]
				binary.LittleEndian.PutUint16(head[:2], uint16(len(owner)))
				w.Write(head[:2])
				w.WriteString(owner)
				w.WriteString(id)
				binary.LittleEndian.PutUint64(head[:], uint64(at.UnixMicro()))
				w.Write(head[:])
				binary.LittleEndian.PutUint32(head[:4], uint32(len(data)))
				w.Write(head[:4])
				_, err := w.Write(data)
				locks[b].Unlock()
				if err != nil {
					return err
				}
				n++
				size += len(data)
			}
			if err := rows.Err(); err != nil {
				return err
			}
			total.Lock()
			images += n
			bytesRead += size
			slog.Info("image pack: read", "table", table, "images", n, "bytes", size, "total", images, "elapsed", time.Since(started).Round(time.Second))
			total.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	for i := range writers {
		if err := writers[i].Flush(); err != nil {
			return err
		}
		files[i].Close()
	}

	dataPath, indexPath := packPaths(dir)
	out, err := os.Create(dataPath + ".tmp")
	if err != nil {
		return err
	}
	defer out.Close()
	w := bufio.NewWriterSize(out, 1<<20)
	ownerIndex := map[string]uint32{}
	var owners []string
	entries := make([]packEntry, 0, images)
	var offset uint64
	type record struct {
		owner, id string
		at        int64
		data      []byte
	}
	for i := range buckets {
		raw, err := os.ReadFile(filepath.Join(tmp, strconv.Itoa(i)))
		if err != nil {
			return err
		}
		var records []record
		for p := 0; p < len(raw); {
			n := int(binary.LittleEndian.Uint16(raw[p:]))
			p += 2
			r := record{owner: string(raw[p : p+n]), id: string(raw[p+n : p+n+36])}
			p += n + 36
			r.at = int64(binary.LittleEndian.Uint64(raw[p:]))
			size := int(binary.LittleEndian.Uint32(raw[p+8:]))
			p += 12
			r.data = raw[p : p+size]
			p += size
			records = append(records, r)
		}
		slices.SortFunc(records, func(a, b record) int {
			return cmp.Or(cmp.Compare(a.owner, b.owner), cmp.Compare(a.at, b.at), cmp.Compare(a.id, b.id))
		})
		for _, r := range records {
			o, ok := ownerIndex[r.owner]
			if !ok {
				o = uint32(len(owners))
				ownerIndex[r.owner] = o
				owners = append(owners, r.owner)
			}
			id, _ := parseImageID(r.id)
			entries = append(entries, packEntry{id: id, owner: o, length: uint32(len(r.data)), offset: offset})
			if _, err := w.Write(r.data); err != nil {
				return err
			}
			offset += uint64(len(r.data))
		}
		os.Remove(filepath.Join(tmp, strconv.Itoa(i)))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	slices.SortFunc(entries, func(a, b packEntry) int { return bytes.Compare(a.id[:], b.id[:]) })

	var index bytes.Buffer
	index.WriteString(packMagic)
	binary.Write(&index, binary.LittleEndian, boundary.UnixMicro())
	binary.Write(&index, binary.LittleEndian, uint32(len(owners)))
	for _, o := range owners {
		binary.Write(&index, binary.LittleEndian, uint16(len(o)))
		index.WriteString(o)
	}
	binary.Write(&index, binary.LittleEndian, uint32(len(entries)))
	record32 := make([]byte, 32)
	for _, e := range entries {
		copy(record32, e.id[:])
		binary.LittleEndian.PutUint32(record32[16:], e.owner)
		binary.LittleEndian.PutUint32(record32[20:], e.length)
		binary.LittleEndian.PutUint64(record32[24:], e.offset)
		index.Write(record32)
	}
	if err := os.WriteFile(indexPath+".tmp", index.Bytes(), 0o644); err != nil {
		return err
	}
	// An index is only valid with the data it was built with.
	if err := os.Remove(indexPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(dataPath+".tmp", dataPath); err != nil {
		return err
	}
	if err := os.Rename(indexPath+".tmp", indexPath); err != nil {
		return err
	}
	slog.Info("image pack: done", "images", len(entries), "owners", len(owners), "bytes", offset, "elapsed", time.Since(started).Round(time.Second))
	return nil
}
