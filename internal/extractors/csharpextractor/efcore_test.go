package csharpextractor

import (
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// storageFacts runs the whole pipeline over a file set and returns the storage
// facts by name, so the tests assert on what a snapshot would contain.
func storageFacts(t *testing.T, files map[string]string) map[string]facts.Fact {
	t.Helper()
	var all []facts.Fact
	var sc aspnetScaffold
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		ff, s := extractFileASTFull([]byte(files[p]), p)
		all = append(all, ff...)
		sc.merge(s)
	}
	all = mergePartialTypes(all)
	resolveCSharpTargets(all)
	out := map[string]facts.Fact{}
	for _, f := range composeStorageFacts(all, sc.storage) {
		out[f.Name] = f
	}
	return out
}

// Reduced from dotnet/eShop's Catalog.API.
var eShopCatalog = map[string]string{
	"src/Catalog.API/Infrastructure/CatalogContext.cs": `
namespace eShop.Catalog.API.Infrastructure;
public class CatalogContext : DbContext
{
    public CatalogContext(DbContextOptions<CatalogContext> options) : base(options) { }
    public required DbSet<CatalogItem> CatalogItems { get; set; }
    public required DbSet<CatalogBrand> CatalogBrands { get; set; }
}`,
	"src/Catalog.API/Infrastructure/EntityConfigurations/CatalogItemEntityTypeConfiguration.cs": `
namespace eShop.Catalog.API.Infrastructure.EntityConfigurations;
class CatalogItemEntityTypeConfiguration : IEntityTypeConfiguration<CatalogItem>
{
    public void Configure(EntityTypeBuilder<CatalogItem> builder)
    {
        builder.ToTable("Catalog");
    }
}`,
	"src/Catalog.API/Model/CatalogItem.cs": `
namespace eShop.Catalog.API.Model;
public class CatalogItem { public int Id { get; set; } }`,
	"src/Catalog.API/Model/CatalogBrand.cs": `
namespace eShop.Catalog.API.Model;
public class CatalogBrand { public int Id { get; set; } }`,
}

func TestStorage_DbContextAndEntities(t *testing.T) {
	got := storageFacts(t, eShopCatalog)

	ctx, ok := got["src/Catalog.API/Infrastructure.CatalogContext"]
	if !ok {
		t.Fatalf("no storage fact for the DbContext; got %v", keysOf(got))
	}
	if ctx.Props["storage_kind"] != "context" || ctx.Props["framework"] != "efcore" {
		t.Errorf("props = %v", ctx.Props)
	}

	// The entity fact is named for the class's OWN declaration, in Model/, not for
	// the Infrastructure/ directory that mentioned it.
	item, ok := got["src/Catalog.API/Model.CatalogItem"]
	if !ok {
		t.Fatalf("entity not attributed to its declaring symbol; got %v", keysOf(got))
	}
	if item.Props["storage_kind"] != "entity" {
		t.Errorf("storage_kind = %v", item.Props["storage_kind"])
	}
	// ToTable is the only place the physical name appears.
	if item.Props["table"] != "Catalog" {
		t.Errorf("table = %v, want Catalog", item.Props["table"])
	}
	if _, ok := got["src/Catalog.API/Model.CatalogBrand"]; !ok {
		t.Error("a DbSet alone should make a type an entity")
	}
}

// The context should point at the entities it owns.
func TestStorage_ContextDependsOnItsEntities(t *testing.T) {
	got := storageFacts(t, eShopCatalog)
	var targets []string
	for _, r := range got["src/Catalog.API/Infrastructure.CatalogContext"].Relations {
		if r.Kind == facts.RelDependsOn {
			targets = append(targets, r.Target)
		}
	}
	sort.Strings(targets)
	if len(targets) != 2 || targets[0] != "src/Catalog.API/Model.CatalogBrand" {
		t.Errorf("depends_on = %v, want both entity symbols", targets)
	}
}

// `DbSet<T>` inside a generic base class names a TYPE PARAMETER. Emitting it
// would create a storage fact called `T` or `TEntity`.
func TestStorage_TypeParametersAreNotEntities(t *testing.T) {
	got := storageFacts(t, map[string]string{
		"src/Data/RepoBase.cs": `
namespace Acme.Data;
public abstract class RepoBase<TEntity> : DbContext where TEntity : class
{
    public DbSet<TEntity> Items { get; set; }
    public DbSet<T> Others { get; set; }
}`,
	})
	for name := range got {
		if name == "src/Data.TEntity" || name == "src/Data.T" {
			t.Errorf("type parameter became an entity: %q", name)
		}
	}
}

// A row type the repository does not declare (a BCL or framework type) has no
// symbol to attach to, so it must not mint a phantom node.
func TestStorage_UndeclaredRowTypeIsDropped(t *testing.T) {
	got := storageFacts(t, map[string]string{
		"src/Data/Repo.cs": `
namespace Acme.Data;
public class Repo
{
    public void Load() { var x = conn.QueryAsync<System.Guid>("SELECT 1"); }
}`,
	})
	for name := range got {
		if name == "src/Data.Guid" {
			t.Error("an undeclared row type must not become a storage fact")
		}
	}
}

func TestStorage_DapperAndMongo(t *testing.T) {
	got := storageFacts(t, map[string]string{
		"src/Data/CipherRepository.cs": `
namespace Acme.Data;
public class CipherRepository
{
    public async Task Load() { await conn.QueryAsync<Cipher>("SELECT * FROM Cipher"); }
}`,
		"src/Data/EventStore.cs": `
namespace Acme.Data;
public class EventStore { private IMongoCollection<AuditEvent> _events; }`,
		"src/Model/Cipher.cs":     "namespace Acme.Model; public class Cipher { }",
		"src/Model/AuditEvent.cs": "namespace Acme.Model; public class AuditEvent { }",
	})
	if f, ok := got["src/Model.Cipher"]; !ok || f.Props["framework"] != "dapper" {
		t.Errorf("Dapper row type = %v", f.Props)
	}
	if f, ok := got["src/Model.AuditEvent"]; !ok || f.Props["framework"] != "mongo" {
		t.Errorf("Mongo collection type = %v", f.Props)
	}
}

func TestStorage_MigrationIsRecorded(t *testing.T) {
	got := storageFacts(t, map[string]string{
		"src/Data/Migrations/Init.cs": `
namespace Acme.Data.Migrations;
public partial class Init : Migration
{
    protected override void Up(MigrationBuilder migrationBuilder) { }
}`,
	})
	f, ok := got["src/Data/Migrations.Init"]
	if !ok {
		t.Fatalf("migration not recorded; got %v", keysOf(got))
	}
	if f.Props["storage_kind"] != "migration" {
		t.Errorf("storage_kind = %v", f.Props["storage_kind"])
	}
}

// Two classes sharing a short name cannot be told apart by name alone, and
// attributing a table to the wrong subsystem is worse than attributing none.
func TestStorage_AmbiguousShortNameIsDropped(t *testing.T) {
	got := storageFacts(t, map[string]string{
		"src/A/Ctx.cs":    "namespace A; public class Ctx : DbContext { public DbSet<Order> Orders { get; set; } }",
		"src/B1/Order.cs": "namespace B1; public class Order { }",
		"src/B2/Order.cs": "namespace B2; public class Order { }",
	})
	for name, f := range got {
		if f.Props["storage_kind"] == "entity" {
			t.Errorf("ambiguous entity %q should have been dropped", name)
		}
	}
}

func keysOf(m map[string]facts.Fact) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
