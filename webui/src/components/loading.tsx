interface LoadingProps {
  message?: string;
  className?: string;
}

/**
 * 正在加载。
 *
 * `role="status"` + `aria-busy` 是这一版补上的：转动的点在无障碍树上等于空白，
 * 不声明的话"正在加载"与"没有数据"对读屏用户是同一件事——页面明明还在取数，
 * 却被念成空的。颜色也从写死的 gray 换成语义 token，免得深色主题下掉出主题。
 */
const Loading = ({ message = '加载中', className = '' }: LoadingProps) => {
  return (
    <div
      role="status"
      aria-busy="true"
      className={`flex flex-col items-center justify-center text-muted-foreground ${className}`}
    >
      <div className="loading-dots" aria-hidden="true">
        <div></div>
        <div></div>
        <div></div>
        <div></div>
      </div>
      <div className="mt-4">
        {message}...
      </div>
    </div>
  );
};

export default Loading;
