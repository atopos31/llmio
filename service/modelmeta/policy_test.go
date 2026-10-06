package modelmeta

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

// setupMetaDB 起一个临时库并跑完整迁移。
//
// 用 models.Init 而不是手写 AutoMigrate 是刻意的：默认策略那一行是**启动时
// 写进去的**（ensureModelAutofillPolicyConfig），只有走一遍真实的初始化，
// "新装的服务读到的策略是什么"才是真的被测了。
func setupMetaDB(t *testing.T) {
	t.Helper()
	prev := models.DB
	prevPath := models.DBPath
	models.Init(context.Background(), filepath.Join(t.TempDir(), "meta.db"))
	t.Cleanup(func() {
		if models.DB != nil {
			// 先关连接再让 t.TempDir 清理（Windows 上文件被占用会导致清理失败）
			if sqlDB, err := models.DB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
		models.DB = prev
		models.DBPath = prevPath
	})
}

func putPolicy(t *testing.T, value string) {
	t.Helper()
	if _, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeyModelAutofillPolicy).
		Update(context.Background(), "value", value); err != nil {
		t.Fatalf("写策略: %v", err)
	}
}

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()

	// Enabled=true 是**刻意的产品选择**：自动填写是纯前端、幂等、可在表单里
	// 逐项复核的动作。它成立的前提是另外三条同时成立。
	if !p.Enabled {
		t.Error("Enabled 默认该是 true")
	}
	// 这三条是"默认启用"的前置条件，改任何一个都是在改产品的风险画像。
	if p.Overwrite {
		t.Error("Overwrite 默认该是 false：只补空，不覆盖用户确认过的值")
	}
	if p.AllowDeprecated {
		t.Error("AllowDeprecated 默认该是 false")
	}
	if !reflect.DeepEqual(p.Sources, consts.ModelMetaDefaultSources) {
		t.Errorf("Sources = %v，期望 %v", p.Sources, consts.ModelMetaDefaultSources)
	}

	// 返回的必须是副本。共用一个底层数组意味着某处一次 `p.Sources[0] = ...`
	// 会改掉包级默认值，而症状是"另一个请求的来源顺序莫名其妙变了"。
	p.Sources[0] = "mutated"
	if DefaultSources[0] == "mutated" {
		t.Error("DefaultPolicy 返回的 Sources 与包级默认值共用底层数组")
	}
	if DefaultPolicy().Sources[0] == "mutated" {
		t.Error("改一份策略污染了后续的默认值")
	}
}

// TestLoadPolicyAfterInit：新装的服务读到的就是那一行默认策略。
func TestLoadPolicyAfterInit(t *testing.T) {
	setupMetaDB(t)

	p := LoadPolicy(context.Background())
	if !p.Enabled || p.Overwrite || p.AllowDeprecated {
		t.Errorf("默认策略 = %+v，期望 (enabled=true, overwrite=false, allow_deprecated=false)", p)
	}
	if !reflect.DeepEqual(p.Sources, consts.ModelMetaDefaultSources) {
		t.Errorf("Sources = %v，期望 %v", p.Sources, consts.ModelMetaDefaultSources)
	}

	// 库里确实落了这一行。"没配过"与"配成开"在数据库里长得一样，
	// 配置页要显示"当前状态：已启用"，那句话必须有出处。
	var count int64
	if err := models.DB.Model(&models.Config{}).
		Where("key = ?", models.KeyModelAutofillPolicy).Count(&count).Error; err != nil {
		t.Fatalf("查配置行: %v", err)
	}
	if count != 1 {
		t.Errorf("配置行数 = %d，期望 1", count)
	}
}

// TestLoadPolicyNeverFails：读不出来的每一种情况都退回默认值。
//
// 调用方是一个只读的建议端点：策略读不出来时，正确的降级是"按默认策略给
// 建议"，而不是让弹窗整个失败。
func TestLoadPolicyNeverFails(t *testing.T) {
	setupMetaDB(t)

	for _, tc := range []struct {
		name  string
		value string
	}{
		{"空值", ``},
		{"只有空白", `   `},
		{"不是 JSON", `not json`},
		{"JSON 但不是对象", `[1,2,3]`},
		{"字段类型不对", `{"enabled":"yes"}`},
		{"sources 是 null", `{"enabled":true,"sources":null}`},
		{"sources 是空数组", `{"enabled":true,"sources":[]}`},
		{"来源全是没见过的", `{"enabled":true,"sources":["nope","also-nope"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putPolicy(t, tc.value)

			p := LoadPolicy(context.Background())
			// 坏值不该把 Sources 留空——留空会让建议循环一次都不跑，
			// 用户看到的是"数据还没准备好"，而真正的原因在日志里。
			if !reflect.DeepEqual(p.Sources, consts.ModelMetaDefaultSources) {
				t.Errorf("Sources = %v，期望退回默认 %v", p.Sources, consts.ModelMetaDefaultSources)
			}
		})
	}
}

func TestLoadPolicyReadsValues(t *testing.T) {
	setupMetaDB(t)

	putPolicy(t, `{"enabled":false,"overwrite":true,"allow_deprecated":true,"sources":["litellm"]}`)

	p := LoadPolicy(context.Background())
	if p.Enabled {
		t.Error("Enabled 该读到 false")
	}
	if !p.Overwrite {
		t.Error("Overwrite 该读到 true")
	}
	if !p.AllowDeprecated {
		t.Error("AllowDeprecated 该读到 true")
	}
	if !reflect.DeepEqual(p.Sources, []string{SourceLiteLLM}) {
		t.Errorf("Sources = %v，期望 [litellm]", p.Sources)
	}
}

// TestLoadPolicyMissingRow：库里没有那一行（比如手工删过）时也照常工作。
func TestLoadPolicyMissingRow(t *testing.T) {
	setupMetaDB(t)

	if err := models.DB.Where("key = ?", models.KeyModelAutofillPolicy).
		Delete(&models.Config{}).Error; err != nil {
		t.Fatalf("删配置行: %v", err)
	}

	p := LoadPolicy(context.Background())
	if !p.Enabled {
		t.Error("缺行时该退回默认的 Enabled=true")
	}
	if !reflect.DeepEqual(p.Sources, consts.ModelMetaDefaultSources) {
		t.Errorf("Sources = %v，期望默认值", p.Sources)
	}
}

func TestNormalizePolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"空列表退回默认", nil, consts.ModelMetaDefaultSources},
		{"去重", []string{"models.dev", "models.dev", "litellm"}, []string{"models.dev", "litellm"}},
		{"丢掉不认识的名字", []string{"models.dev", "openrouter", "litellm"}, []string{"models.dev", "litellm"}},
		{"去掉空白", []string{"  models.dev  "}, []string{"models.dev"}},
		{"丢掉空串", []string{"", "litellm", "   "}, []string{"litellm"}},
		// 顺序是**语义**：按序尝试，第一个命中的就是结论。归一化不能排序。
		{"保持顺序", []string{"litellm", "models.dev"}, []string{"litellm", "models.dev"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := normalizePolicy(models.ModelAutofillPolicy{Sources: tc.in})
			if !reflect.DeepEqual(p.Sources, tc.want) {
				t.Errorf("Sources = %v，期望 %v", p.Sources, tc.want)
			}
		})
	}
}

// TestNormalizePolicyDoesNotAliasDefaults：归一化产出的 Sources 不能与
// 包级默认值共用底层数组。
func TestNormalizePolicyDoesNotAliasDefaults(t *testing.T) {
	p := normalizePolicy(models.ModelAutofillPolicy{})
	p.Sources[0] = "mutated"
	if DefaultSources[0] == "mutated" {
		t.Error("归一化结果与 DefaultSources 共用底层数组")
	}
}

// TestNormalizePolicyKeepsOtherFields：它只管来源列表，别的字段原样穿过去。
func TestNormalizePolicyKeepsOtherFields(t *testing.T) {
	p := normalizePolicy(models.ModelAutofillPolicy{
		Enabled: true, Overwrite: true, AllowDeprecated: true,
		Sources: []string{"litellm"},
	})
	if !p.Enabled || !p.Overwrite || !p.AllowDeprecated {
		t.Errorf("其余字段被改动了：%+v", p)
	}
}
