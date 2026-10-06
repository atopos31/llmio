import { useCallback, useEffect, useRef, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useFieldArray, useForm } from "react-hook-form";
import { z } from "zod";
import { configAPI, createModelProvider, defaultModelAutofillPolicy, getModelMetadata, updateModelProvider } from "@/lib/api";
import type { Model, ModelAutofillPolicy, ModelMetadataCandidate, ModelMetadataSuggestion, ModelWithProvider, ProviderModel } from "@/lib/api";
import {
  AUTOFILL_FIELDS,
  classifyFailure,
  computeAutofill,
  isUsable,
  suggestionFromCandidate,
} from "@/lib/model-autofill";
import type { AutofillFailureReason, AutofillField, AutofillReport, AutofillSnapshot } from "@/lib/model-autofill";
import { termsFormToPayload, termsToForm, validateTermsForm } from "@/lib/peak";
import type { PeakTermsForm } from "@/lib/peak";
import { toast } from "sonner";

const AUTOFILL_CONFIG_KEY = "model_autofill_policy";

/**
 * 自动预填的防抖时长。
 *
 * 上游模型名是一个可自由输入的文本框，而预填是按「上游 + 模型名」拉取建议的
 * ——不防抖就是每敲一个字符打一次端点。"claude-sonnet-4-5" 是 17 次。
 */
const AUTOFILL_DEBOUNCE_MS = 400;

/**
 * 数据源还没准备好时的重试节奏与上限。
 *
 * 首次抓取要下 5 MB（压缩后约 531 KB）并在后台建索引，此时端点回的是
 * `catalog_unavailable`（"稍后重试就能好"），而不是失败。所以这一条要自动
 * 重试几次；但也必须封顶——一直转圈比一句"稍后再试"更让人不知道该怎么办。
 */
const AUTOFILL_RETRY_MS = 5000;
const AUTOFILL_MAX_RETRIES = 3;

/** 上一次预填的状态，全部给界面用。 */
export type ModelAutofillState = {
  /** 策略里总开关的状态 */
  enabled: boolean;
  /** 策略里是否允许覆盖已有值——它决定"已有值未覆盖"那句提示给不给退路 */
  overwrite: boolean;
  loading: boolean;
  /** 命中的建议；未命中或还没查时为 null */
  suggestion: ModelMetadataSuggestion | null;
  /** 未命中的原因；命中时为 null */
  failure: AutofillFailureReason | null;
  /** 端点本身失败（网络 / 500）的原文。与上面那个 failure 是两件事，文案不同 */
  error: string | null;
  /** 上一次预填的结论 */
  report: AutofillReport | null;
  /** 这次结论来自用户点「重新填写」或「采用」，而不是自动预填 */
  manual: boolean;
};

/** 预填会写的格子：六个能力/价格字段，外带跟着价格一起改的币种。 */
type AutofillWritableField = AutofillField | "currency";

const emptyAutofill = (enabled: boolean, overwrite: boolean): ModelAutofillState => ({
  enabled,
  overwrite,
  loading: false,
  suggestion: null,
  failure: null,
  error: null,
  report: null,
  manual: false,
});

const headerPairSchema = z.object({
  key: z.string().min(1, { message: "请求头键不能为空" }),
  value: z.string().default(""),
});

export const modelProviderFormSchema = z.object({
  model_id: z.number().positive({ message: "模型ID必须大于0" }),
  provider_name: z.string().min(1, { message: "提供商模型名称不能为空" }),
  provider_id: z.number().positive({ message: "提供商ID必须大于0" }),
  tool_call: z.boolean(),
  structured_output: z.boolean(),
  image: z.boolean(),
  with_header: z.boolean(),
  weight: z.number().positive({ message: "权重必须大于0" }),
  customer_headers: z.array(headerPairSchema).default([]),
  extra_body: z.string().default(""),
  input_price: z.number().min(0).default(0),
  cache_read_price: z.number().min(0).default(0),
  output_price: z.number().min(0).default(0),
  currency: z.enum(["CNY", "USD"]).default("CNY"),
});

export type ModelProviderFormValues = z.input<typeof modelProviderFormSchema>;

type UseModelProviderFormParams = {
  selectedModelId: number | null;
  models: Model[];
  providerModelsMap: Record<number, ProviderModel[]>;
  loadProviderModels: (providerId: number, force?: boolean) => Promise<void>;
  onReload: (modelId: number) => Promise<void> | void;
};

export const useModelProviderForm = ({
  selectedModelId,
  models,
  providerModelsMap,
  loadProviderModels,
  onReload,
}: UseModelProviderFormParams) => {
  const [open, setOpen] = useState(false);
  const [editingAssociation, setEditingAssociation] = useState<ModelWithProvider | null>(null);
  const [showProviderModels, setShowProviderModels] = useState(false);
  /**
   * 峰谷条款，null = 这条关联没配（后端那一列落 NULL，按基础价计费）。
   *
   * 与 RHF 那份表单并列放在这里而不是塞进 modelProviderFormSchema：条款是
   * 可增删的时段数组，zod schema 给它只能编成一串嵌套字段，而校验要报的是
   * "第几段"（见 lib/peak.ts 的 validateTermsForm），渲染时又得从字段路径
   * 翻译回去。存成独立的 state，校验结论原样就是界面要的字。
   */
  const [peakTerms, setPeakTerms] = useState<PeakTermsForm | null>(null);
  /** 保存被条款校验拦下的次数。>0 时条款编辑器才开始显示红色（它据此判断"用户试过了"）。 */
  const [peakSubmitAttempt, setPeakSubmitAttempt] = useState(0);

  const getDefaultFormValues = (overrideModelId?: number): ModelProviderFormValues => {
    const fallbackModelId = overrideModelId ?? selectedModelId ?? models[0]?.ID ?? 0;
    return {
      model_id: fallbackModelId,
      provider_name: "",
      provider_id: 0,
      tool_call: false,
      structured_output: false,
      image: false,
      with_header: false,
      weight: 1,
      customer_headers: [],
      extra_body: "",
      input_price: 0,
      cache_read_price: 0,
      output_price: 0,
      currency: "CNY",
    };
  };

  const form = useForm<ModelProviderFormValues>({
    resolver: zodResolver(modelProviderFormSchema),
    defaultValues: getDefaultFormValues(),
  });

  const { fields: headerFields, append: appendHeader, remove: removeHeader } = useFieldArray({
    control: form.control,
    name: "customer_headers",
  });

  const selectedProviderId = form.watch("provider_id");
  const watchedProviderModel = form.watch("provider_name");

  useEffect(() => {
    if (selectedProviderId && selectedProviderId > 0) {
      loadProviderModels(selectedProviderId);
    }
    setShowProviderModels(false);
  }, [selectedProviderId, loadProviderModels]);

  // -------------------------------------------------------------------------
  // 自动填写（§5.1）
  //
  // 这里是自动填写的**唯一入口**：写入路径完全沿用既有的 Create/Update，
  // 服务端不做任何补值（§4.2）。所有保护都在下面这几步里，一步都不能少：
  // 总开关、防抖、同一目标不重复拉、源没给的不写、已有值/用户改过的不覆盖、
  // 换目标时把上一次预填的痕迹撤回。
  // -------------------------------------------------------------------------
  const [autofillPolicy, setAutofillPolicy] = useState<ModelAutofillPolicy>(defaultModelAutofillPolicy);
  const [autofill, setAutofill] = useState<ModelAutofillState>(() =>
    emptyAutofill(defaultModelAutofillPolicy.enabled, defaultModelAutofillPolicy.overwrite),
  );
  const [policyLoaded, setPolicyLoaded] = useState(false);
  /** 已经预填过的「上游 + 模型名」，同一组不重复拉 */
  const filledTargetRef = useRef("");
  /**
   * 上一次**看到**的「上游 + 模型名」。
   *
   * 与 `filledTargetRef` 是两件事：那个记的是"拉过了"，这个记的是"表单里现在
   * 摆着的值是为哪一组填的"。判断有没有换目标要靠后者——用户完全可能在第一次
   * 查询（防抖 400ms）落地之前就把模型名改掉，那时 filledTargetRef 还是空的，
   * 而表单里的值已经是上一组的了。
   */
  const seenTargetRef = useRef("");
  /**
   * 预填自己写过的格子，以及**写之前**它长什么样。
   *
   * 换目标时要把这些格子撤回原样：那些值描述的是上一个目标，留在表单里就是
   * 一条没人验证过的断言（"这个上游支持视觉"），而它长得和用户自己填的值
   * 一模一样——`setValue` 不标脏，dirtyFields 也认不出来。所以只能由写的人
   * 自己记着，见 rollbackWritten。
   */
  const writtenRef = useRef(new Map<AutofillWritableField, boolean | number | string>());
  /** 已自动重试的次数，用于给 catalog_unavailable 封顶 */
  const autofillRetryRef = useRef(0);
  /** 重试计数器的自增位：让下面那个 effect 重新跑一次 */
  const [autofillRetry, setAutofillRetry] = useState(0);

  /**
   * 读三个能力与三档价格的当前值。
   *
   * 用 `getValues()` 而不是渲染期订阅：这段只在拉完建议要写之前读一次，
   * 订阅会让价格输入框每敲一个字符重渲染整个页面。
   */
  const readAutofillSnapshot = useCallback((): AutofillSnapshot => {
    const values = form.getValues();
    return {
      tool_call: Boolean(values.tool_call),
      structured_output: Boolean(values.structured_output),
      image: Boolean(values.image),
      input_price: Number(values.input_price ?? 0),
      cache_read_price: Number(values.cache_read_price ?? 0),
      output_price: Number(values.output_price ?? 0),
      currency: values.currency ?? "CNY",
    };
  }, [form]);

  /**
   * 用户在这次弹窗里手工改过的字段。
   *
   * 靠 RHF 的 dirtyFields，而不是自己挂 onChange：六个字段分别由 FormField
   * 渲染，自己记就得把这个 setter 穿到每一个 Checkbox 与 Input 上，漏一个
   * 就是一个"输入被改掉"的位置。`setValue` 默认不标脏，所以预填自己写进去
   * 的值不会被当成"用户改过"——这正是要的：下次预填还能覆盖它。
   */
  const readEditedFields = useCallback((): Set<AutofillField> => {
    const dirty = form.formState.dirtyFields as Record<string, unknown>;
    return new Set(AUTOFILL_FIELDS.filter((field) => Boolean(dirty[field])));
  }, [form]);

  /**
   * 读某一格当前的值，缺省按表单的默认（能力 false / 价格 0 / 币种 CNY）。
   * 取值口径与 `readAutofillSnapshot` 一致，撤回时才不会把一个 undefined
   * 写回表单。
   */
  const readAutofillField = useCallback(
    (field: AutofillWritableField): boolean | number | string => {
      if (field === "currency") {
        return form.getValues("currency") ?? "CNY";
      }
      if (field === "tool_call" || field === "structured_output" || field === "image") {
        return Boolean(form.getValues(field));
      }
      return Number(form.getValues(field) ?? 0);
    },
    [form],
  );

  /** 把算好的结果写进表单，顺带记下每格被覆盖前的样子。 */
  const applyAutofillResult = useCallback(
    (result: ReturnType<typeof computeAutofill>) => {
      const written = writtenRef.current;
      // 只在第一次写某格时记：再次预填覆盖的是自己上一次写的值，撤回要回到
      // 更早那个"本来就在表单里的值"，不是回到上一次的预填结果。
      const remember = (field: AutofillWritableField) => {
        if (!written.has(field)) written.set(field, readAutofillField(field));
      };

      for (const { field, value } of result.fill) {
        remember(field);
        // 分两支是为了让 setValue 的类型收窄到具体字段名；写成一句会用
        // 联合类型的 setter，TS 解不出来。
        if (field === "tool_call" || field === "structured_output" || field === "image") {
          form.setValue(field, value as boolean);
        } else {
          form.setValue(field, value as number);
        }
      }
      if (result.currency) {
        remember("currency");
        form.setValue("currency", result.currency as "CNY" | "USD");
      }
    },
    [form, readAutofillField],
  );

  /**
   * 撤回预填写过的格子（换目标时调）。
   *
   * 用户手改过的格子不动：那已经是他自己的值了，与是哪个目标无关。撤回后清空
   * 记录——下一次预填重新开始记，否则会把"撤回后的样子"当成更早的基线。
   */
  const rollbackWritten = useCallback(() => {
    const written = writtenRef.current;
    if (written.size === 0) return;
    const dirty = form.formState.dirtyFields as Record<string, unknown>;
    written.forEach((value, field) => {
      if (dirty[field]) return;
      if (field === "currency") {
        form.setValue("currency", value as "CNY" | "USD");
      } else if (field === "tool_call" || field === "structured_output" || field === "image") {
        form.setValue(field, value as boolean);
      } else {
        form.setValue(field, value as number);
      }
    });
    written.clear();
  }, [form]);

  /**
   * 按一份建议预填。自动与手动走的是同一条路，区别只在 `trigger`。
   *
   * 端点失败（网络 / 500）与"源里没有"分开记：前者是"这次查询没成"，后者是
   * "查到了，确实没有"，两者对用户是两件事（§5.2）。
   */
  const applySuggestion = useCallback(
    (
      suggestion: ModelMetadataSuggestion,
      trigger: "auto" | "manual",
      targetChanged = false,
    ): AutofillReport => {
      const result = computeAutofill({
        suggestion,
        current: readAutofillSnapshot(),
        edited: readEditedFields(),
        policy: autofillPolicy,
        trigger,
        targetChanged,
      });
      applyAutofillResult(result);
      return {
        suggestion,
        result,
        providerLabel: suggestion.provider_name || suggestion.provider || "",
        modelLabel: suggestion.model || "",
      };
    },
    [applyAutofillResult, autofillPolicy, readAutofillSnapshot, readEditedFields],
  );

  const runAutofill = useCallback(
    async (
      providerId: number,
      providerModel: string,
      trigger: "auto" | "manual",
      targetChanged = false,
    ) => {
      // 换了目标就先撤痕迹，再去查新的：这一步与查询结果无关，所以放在这里
      // 而不是"查到之后再撤"。若新目标查不到东西，表单留给用户的应该是
      // "关于它我们什么都不知道"，而不是上一个目标的值。
      if (targetChanged) rollbackWritten();
      setAutofill((prev) => ({ ...prev, loading: true, error: null }));
      try {
        const suggestion = await getModelMetadata(providerId, providerModel);
        if (!isUsable(suggestion)) {
          const failure = classifyFailure(suggestion);
          setAutofill((prev) => ({
            ...prev,
            loading: false,
            // 未命中也要留着这份响应：跨上游候选就挂在它上面，而候选只在
            // 未命中时才有（§4.1.1）。丢掉它等于把候选一起丢掉。
            suggestion,
            failure,
            error: null,
            report: null,
            manual: trigger === "manual",
          }));
          // 数据源还在准备：等一会儿自己再试，试满上限就停在这个提示上。
          if (failure === "catalog_unavailable" && autofillRetryRef.current < AUTOFILL_MAX_RETRIES) {
            autofillRetryRef.current += 1;
            window.setTimeout(() => {
              filledTargetRef.current = "";
              setAutofillRetry((n) => n + 1);
            }, AUTOFILL_RETRY_MS);
          }
          return;
        }
        autofillRetryRef.current = 0;
        const report = applySuggestion(suggestion, trigger, targetChanged);
        setAutofill((prev) => ({
          ...prev,
          loading: false,
          suggestion,
          failure: null,
          error: null,
          report,
          manual: trigger === "manual",
        }));
      } catch (err) {
        // 失败不阻塞保存：自动填写是便利功能，不能成为保存的前置条件（§5.2）。
        console.error("自动填写查询失败：", err);
        setAutofill((prev) => ({
          ...prev,
          loading: false,
          suggestion: null,
          failure: null,
          error: err instanceof Error ? err.message : String(err),
          report: null,
          manual: trigger === "manual",
        }));
      }
    },
    [applySuggestion, rollbackWritten],
  );

  /**
   * 读策略。第一次打开弹窗时读一次。
   *
   * **读不到时按默认（开）处理**，不静默改成"关"：自动填写只往用户正看着的
   * 表单里预填，保存前可以逐项复核，所以降级成"开"是安全的；降级成"关"则会
   * 让开关明明开着却什么都不发生，而且界面上没有任何地方说得清为什么。
   * 后端读策略失败时也是同一个选择（见 service/modelmeta/policy.go）。
   */
  useEffect(() => {
    if (!open || policyLoaded) return;
    setPolicyLoaded(true);
    configAPI
      .getConfig(AUTOFILL_CONFIG_KEY)
      .then((response) => {
        if (!response.value) return;
        const parsed = JSON.parse(response.value) as Partial<ModelAutofillPolicy>;
        const next: ModelAutofillPolicy = {
          ...defaultModelAutofillPolicy,
          ...parsed,
          sources: parsed.sources?.length ? parsed.sources : defaultModelAutofillPolicy.sources,
        };
        setAutofillPolicy(next);
        setAutofill((prev) => ({ ...prev, enabled: next.enabled, overwrite: next.overwrite }));
      })
      .catch((err) => {
        console.warn("读取自动填写策略失败，按默认策略处理：", err);
      });
  }, [open, policyLoaded]);

  /**
   * 选定「上游 + 上游模型」后自动拉取并预填。
   *
   * 同一组输入只拉一次（`filledTargetRef`）：这个 effect 会因为开关、重试
   * 计数器等无关变化重跑，而重跑一次就多打一次端点。
   *
   * 换没换目标也在这里判：这个 effect 每次重跑读到的都是**最新**的表单值，
   * 用户改一个字符这里就看得出来。
   */
  useEffect(() => {
    if (!open || !autofillPolicy.enabled) return;
    const model = (watchedProviderModel || "").trim();
    if (!selectedProviderId || selectedProviderId <= 0 || !model) return;

    const target = `${selectedProviderId}\u0000${model}`;
    if (filledTargetRef.current === target) return;

    // 换没换目标看的是上一次**显示**的是哪一组，不是上一次查的是哪一组：
    // 用户在第一次查询落地前就把模型名改掉时，`filledTargetRef` 还是空的。
    // 空串（弹窗刚打开、上游或模型名被清空）不算换目标——那时表单里没有任何
    // 一组值可以作废。
    const switched = seenTargetRef.current !== "" && seenTargetRef.current !== target;
    seenTargetRef.current = target;

    const timer = window.setTimeout(() => {
      filledTargetRef.current = target;
      void runAutofill(selectedProviderId, model, "auto", switched);
    }, AUTOFILL_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [open, autofillPolicy.enabled, selectedProviderId, watchedProviderModel, autofillRetry, runAutofill]);

  /** 「重新填写」：用户的显式动作，因此不看 Overwrite 与"改过"这两道门。 */
  const refreshAutofill = useCallback(() => {
    const model = (form.getValues("provider_name") || "").trim();
    const providerId = form.getValues("provider_id");
    if (!providerId || providerId <= 0 || !model) return;
    filledTargetRef.current = `${providerId}\u0000${model}`;
    autofillRetryRef.current = 0;
    void runAutofill(providerId, model, "manual");
  }, [form, runAutofill]);

  /** 「采用」某条跨上游候选：到这一步用户已经明确要按那家上游填（§4.1.1）。 */
  const adoptCandidate = useCallback(
    (candidate: ModelMetadataCandidate) => {
      const report = applySuggestion(suggestionFromCandidate(candidate), "manual");
      setAutofill((prev) => ({
        ...prev,
        loading: false,
        suggestion: null,
        failure: null,
        error: null,
        report,
        manual: true,
      }));
    },
    [applySuggestion],
  );

  const resetAutofill = useCallback(() => {
    filledTargetRef.current = "";
    seenTargetRef.current = "";
    autofillRetryRef.current = 0;
    // 这次弹窗的痕迹不带到下一次：留着的话，下一个弹窗里第一次预填会被当成
    // "换了目标"，刚填好的值立刻又被撤回。
    writtenRef.current.clear();
    setAutofill((prev) => ({ ...emptyAutofill(prev.enabled, prev.overwrite), loading: false }));
  }, []);

  const buildPayload = (values: ModelProviderFormValues, peak: PeakTermsForm | null) => {
    const headers: Record<string, string> = {};
    (values.customer_headers || []).forEach(({ key, value }) => {
      const trimmedKey = key.trim();
      if (trimmedKey) {
        headers[trimmedKey] = value ?? "";
      }
    });

    let extraBody: Record<string, unknown> = {};
    if (values.extra_body && values.extra_body.trim()) {
      try {
        extraBody = JSON.parse(values.extra_body);
      } catch {
        // ignore parse errors, send empty object
      }
    }

    return {
      model_id: values.model_id,
      provider_name: values.provider_name,
      provider_id: values.provider_id,
      tool_call: values.tool_call,
      structured_output: values.structured_output,
      image: values.image,
      with_header: values.with_header,
      customer_headers: headers,
      extra_body: extraBody,
      weight: values.weight,
      input_price: values.input_price ?? 0,
      cache_read_price: values.cache_read_price ?? 0,
      output_price: values.output_price ?? 0,
      currency: values.currency ?? "CNY",
      /**
       * 无条件带上 peak，null 也带。
       *
       * 更新接口把"省略 peak"和"peak: null"都当作清空（后端在结构体更新之外
       * 补了一次显式清空，否则 GORM 会跳过 nil 指针、旧条款一直留在行上），
       * 所以这两者在这里没有区别；但显式传 null 让"移除峰谷配置"这条路径在
       * 请求体里就看得见，而不是要读一遍后端代码才知道省略等于清空。
       */
      peak: peak ? termsFormToPayload(peak) : null,
    };
  };

  const openEditDialog = (association: ModelWithProvider) => {
    setEditingAssociation(association);
    const headerPairs = Object.entries(association.CustomerHeaders || {}).map(([key, value]) => ({
      key,
      value,
    }));
    let extraBodyStr = "";
    if (association.ExtraBody && Object.keys(association.ExtraBody).length > 0) {
      extraBodyStr = JSON.stringify(association.ExtraBody, null, 2);
    }
    form.reset({
      model_id: association.ModelID,
      provider_name: association.ProviderModel,
      provider_id: association.ProviderID,
      tool_call: association.ToolCall,
      structured_output: association.StructuredOutput,
      image: association.Image,
      with_header: association.WithHeader,
      weight: association.Weight,
      customer_headers: headerPairs.length ? headerPairs : [],
      extra_body: extraBodyStr,
      input_price: association.InputPrice ?? 0,
      cache_read_price: association.CacheReadPrice ?? 0,
      output_price: association.OutputPrice ?? 0,
      currency: (association.Currency as "CNY" | "USD") || "CNY",
    });
    // 条款跟着这条关联走：没配就是 null（三态里的"未配置"），配了才回填。
    // 用 termsToForm 而不是直接塞 association.Peak：乘数在表单里是字符串
    // （见 lib/peak.ts 的 PeakPeriodForm），"8:30" 也要顺手归一成 "08:30"。
    setPeakTerms(association.Peak ? termsToForm(association.Peak) : null);
    // 上一次保存被拦下的红色不能跟着飘到这一次打开：用户会以为新打开的这一条
    // 就有问题
    setPeakSubmitAttempt(0);
    resetAutofill();
    setOpen(true);
  };

  const openCreateDialog = (modelId?: number) => {
    setEditingAssociation(null);
    form.reset(getDefaultFormValues(modelId));
    setPeakTerms(null);
    setPeakSubmitAttempt(0);
    resetAutofill();
    setOpen(true);
  };

  const submit = async (values: ModelProviderFormValues) => {
    if (peakTerms) {
      const issues = validateTermsForm(peakTerms);
      if (issues.length > 0) {
        // 记一次尝试，条款编辑器据此把问题列出来并滚进视野；
        // 这里只负责拦，文案与呈现都在那边（它才知道是哪一段）
        setPeakSubmitAttempt((n) => n + 1);
        return;
      }
    }
    try {
      if (editingAssociation) {
        await updateModelProvider(editingAssociation.ID, buildPayload(values, peakTerms));
        toast.success("关联管理更新成功");
        setEditingAssociation(null);
      } else {
        await createModelProvider(buildPayload(values, peakTerms));
        toast.success("关联管理创建成功");
      }

      setOpen(false);
      form.reset(getDefaultFormValues());
      setPeakTerms(null);
      await onReload(values.model_id);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`${editingAssociation ? "更新" : "创建"}关联管理失败: ${message}`);
      console.error(err);
    }
  };

  const sortProviderModels = (providerId: number, query: string): ProviderModel[] => {
    const modelsForProvider = providerModelsMap[providerId] || [];
    if (!query) return modelsForProvider;

    const normalized = query.toLowerCase();
    const score = (id: string) => {
      const val = id.toLowerCase();
      if (val === normalized) return 1000;
      let s = 0;
      if (val.startsWith(normalized)) s += 500;
      if (val.includes(normalized)) s += 200;
      s -= Math.abs(val.length - normalized.length);
      return s;
    };

    return [...modelsForProvider].sort((a, b) => score(b.id) - score(a.id));
  };

  return {
    form,
    open,
    setOpen,
    editingAssociation,
    showProviderModels,
    setShowProviderModels,
    headerFields,
    appendHeader,
    removeHeader,
    selectedProviderId,
    peakTerms,
    setPeakTerms,
    peakSubmitAttempt,
    autofill,
    refreshAutofill,
    adoptCandidate,
    openEditDialog,
    openCreateDialog,
    submit,
    sortProviderModels,
  };
};
