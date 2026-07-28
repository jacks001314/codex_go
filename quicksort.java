import java.util.Arrays;

public class quicksort {
    public static void quickSort(int[] numbers) {
        if (numbers == null || numbers.length < 2) {
            return;
        }

        quickSort(numbers, 0, numbers.length - 1);
    }

    private static void quickSort(int[] numbers, int left, int right) {
        int i = left;
        int j = right;
        int pivot = numbers[left + (right - left) / 2];

        while (i <= j) {
            while (numbers[i] < pivot) {
                i++;
            }
            while (numbers[j] > pivot) {
                j--;
            }

            if (i <= j) {
                int temp = numbers[i];
                numbers[i] = numbers[j];
                numbers[j] = temp;
                i++;
                j--;
            }
        }

        if (left < j) {
            quickSort(numbers, left, j);
        }
        if (i < right) {
            quickSort(numbers, i, right);
        }
    }

    public static void main(String[] args) {
        int[] numbers;

        if (args.length == 0) {
            numbers = new int[] { 9, 4, 7, 3, 10, 5, 1, 8, 2, 6 };
        } else {
            numbers = Arrays.stream(args)
                    .mapToInt(Integer::parseInt)
                    .toArray();
        }

        System.out.println("Before: " + Arrays.toString(numbers));
        quickSort(numbers);
        System.out.println("After:  " + Arrays.toString(numbers));
    }
}
