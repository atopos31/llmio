import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";

export default function LoginPage() {
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
    <div className="flex items-center justify-center min-h-screen bg-background">
      <Card className="w-full max-w-sm sm:m-0 m-5">
        <CardHeader>
          <CardTitle className="text-2xl">登录</CardTitle>
          <CardDescription>
            输入您的访问令牌以访问系统
          </CardDescription>
        </CardHeader>
        <form onSubmit={handleLogin}>
          <CardContent className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="token">访问令牌</Label>
              <Input 
                id="token" 
                type="password" 
                value={token} 
                onChange={(e) => setToken(e.target.value)} 
                placeholder="输入您的访问令牌"
                required
              />
            </div>
          </CardContent>
          <CardFooter>
            <Button className="w-full mt-5" type="submit">登录</Button>
          </CardFooter>
        </form>
      </Card>
    </div>
  );
}