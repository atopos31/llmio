import { useRef, useState } from "react";
import { fetchEventSource } from "@microsoft/fetch-event-source";
import { testModelProvider } from "@/lib/api";

type TestResultState = Record<number, { loading: boolean; result: any }>;

type ReactTestState = {
  loading: boolean;
  messages: string;
  success: boolean | null;
  error: string | null;
};

const defaultReactTestState: ReactTestState = {
  loading: false,
  messages: "",
  success: null,
  error: null,
};

export const useModelProviderTesting = () => {
  const [testResults, setTestResults] = useState<TestResultState>({});
  const [testDialogOpen, setTestDialogOpen] = useState(false);
  const [selectedTestId, setSelectedTestId] = useState<number | null>(null);
  const [testType, setTestType] = useState<"connectivity" | "react">("connectivity");
  const [reactTestResult, setReactTestResult] = useState<ReactTestState>(defaultReactTestState);
  const currentControllerRef = useRef<AbortController | null>(null);

  const openTestDialog = (id: number) => {
    currentControllerRef.current?.abort();
    setSelectedTestId(id);
    setTestType("connectivity");
    setTestDialogOpen(true);
    setReactTestResult(defaultReactTestState);
  };

  const closeTestDialog = () => {
    currentControllerRef.current?.abort();
    setTestDialogOpen(false);
  };

  const handleConnectivityTest = async (id: number) => {
    try {
      setTestResults((prev) => ({
        ...prev,
        [id]: { loading: true, result: null },
      }));

      const result = await testModelProvider(id);
      setTestResults((prev) => ({
        ...prev,
        [id]: { loading: false, result },
      }));
      return result;
    } catch (err) {
      setTestResults((prev) => ({
        ...prev,
        [id]: { loading: false, result: { error: `测试失败${err}` } },
      }));
      console.error(err);
      return { error: `测试失败${err}` };
    }
  };

  const handleReactTest = async (id: number) => {
    setReactTestResult((prev) => ({
      ...prev,
      messages: "",
      loading: true,
      success: null,
      error: null,
    }));

    try {
      const token = localStorage.getItem("authToken");
      const controller = new AbortController();
      currentControllerRef.current = controller;

      await fetchEventSource(`/api/test/react/${id}`, {
        method: "GET",
        headers: {
          Authorization: `Bearer ${token}`,
        },
        signal: controller.signal,
        onmessage(event) {
          setReactTestResult((prev) => {
            if (event.event === "start") {
              return {
                ...prev,
                messages: `${prev.messages}[开始测试] ${event.data}\n`,
              };
            }
            if (event.event === "toolcall") {
              return {
                ...prev,
                messages: `${prev.messages}\n[调用工具] ${event.data}\n`,
              };
            }
            if (event.event === "toolres") {
              return {
                ...prev,
                messages: `${prev.messages}\n[工具输出] ${event.data}\n`,
              };
            }
            if (event.event === "message") {
              if (event.data.trim()) {
                return {
                  ...prev,
                  messages: `${prev.messages}${event.data}`,
                };
              }
            }
            if (event.event === "error") {
              return {
                ...prev,
                success: false,
                messages: `${prev.messages}\n[错误] ${event.data}\n`,
              };
            }
            if (event.event === "success") {
              return {
                ...prev,
                success: true,
                messages: `${prev.messages}\n[成功] ${event.data}`,
              };
            }
            return prev;
          });
        },
        onclose() {
          setReactTestResult((prev) => ({
            ...prev,
            loading: false,
          }));
        },
        onerror(err) {
          setReactTestResult((prev) => ({
            ...prev,
            loading: false,
            error: err.message || "测试过程中发生错误",
            success: false,
          }));
          throw err;
        },
      });
    } catch (err) {
      setReactTestResult((prev) => ({
        ...prev,
        loading: false,
        error: "测试失败",
        success: false,
      }));
      console.error(err);
    }
  };

  const executeTest = async () => {
    if (!selectedTestId) return;

    if (testType === "connectivity") {
      await handleConnectivityTest(selectedTestId);
      return;
    }

    await handleReactTest(selectedTestId);
  };

  return {
    testResults,
    testDialogOpen,
    setTestDialogOpen,
    selectedTestId,
    testType,
    setTestType,
    reactTestResult,
    openTestDialog,
    closeTestDialog,
    executeTest,
  };
};
