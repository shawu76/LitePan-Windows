import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";

type TmdbTestResult = { ok?: boolean; api_ok?: boolean; image_ok?: boolean };

export async function runTmdbTest(request: () => Promise<TmdbTestResult>) {
  try {
    const result = await request();
    const apiOK = result.api_ok ?? result.ok;
    const imageOK = result.image_ok ?? true;
    if (apiOK && imageOK) toast.success("TMDB 连通正常：API ✓ 图片 ✓");
    else if (apiOK) toast.error("TMDB 部分异常：API ✓ 图片 ×");
    else if (imageOK) toast.error("TMDB 部分异常：API × 图片 ✓");
    else toast.error("TMDB 全部异常：API × 图片 ×");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "TMDB 测试失败"));
  }
}
