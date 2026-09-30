import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";

export default function LoginPage() {
  const { t } = useTranslation('login');
  const [token, setToken] = useState("");
  const navigate = useNavigate();

  const handleLogin = (e: React.FormEvent) => {
    e.preventDefault();
    if (token.trim()) {
      localStorage.setItem("authToken", token);
      // Redirect to home page after login
      navigate("/");
    }
  };

  return (
    // 登录页在外壳之外，因此它得自己管滚动：文档被锁死了（见 index.css）。
    // 竖直居中用卡片上的 my-auto 而不是容器的 items-center——内容比视口高时，
    // items-center 会把顶部切掉且滚不到，auto 外边距则会退化成 0。
    <div className="flex h-dvh justify-center overflow-y-auto bg-background p-5">
      <Card className="my-auto w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-2xl">{t('title')}</CardTitle>
          <CardDescription>
            {t('description')}
          </CardDescription>
        </CardHeader>
        <form onSubmit={handleLogin}>
          <CardContent className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="token">{t('token_label')}</Label>
              <Input
                id="token"
                type="password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder={t('token_placeholder')}
                required
              />
            </div>
          </CardContent>
          <CardFooter>
            <Button className="w-full mt-5" type="submit">{t('submit')}</Button>
          </CardFooter>
        </form>
      </Card>
    </div>
  );
}